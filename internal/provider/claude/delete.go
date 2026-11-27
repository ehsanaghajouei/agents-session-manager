package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/uuid"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
	"github.com/ehsanaghajouei/agents-session-manager/internal/proc"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
)

// Delete permanently removes the three session-owned locations used by Export.
func (p *Provider) Delete(ctx context.Context, ex host.Executor, s provider.Session) error {
	if !ex.IsLocal() {
		return ErrRemote
	}
	return p.deleteSession(ctx, s, processMatches)
}

func (p *Provider) deleteSession(ctx context.Context, s provider.Session, matches func(int, string) (bool, error)) error {
	if id, err := uuid.Parse(s.ID); err != nil || id.String() != s.ID {
		return errors.New("invalid session ID")
	}
	rel, err := filepath.Rel(p.ProjectsDir(), s.Path)
	project, transcript := filepath.Dir(rel), filepath.Base(rel)
	if err != nil || !filepath.IsLocal(rel) || project == "." ||
		filepath.Base(project) != project || transcript != s.ID+".jsonl" {
		return errors.New("session has no safe project transcript")
	}
	root, err := os.OpenRoot(p.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	projects, err := openDeleteDir(root, "projects")
	if err != nil {
		return err
	}
	defer projects.Close()
	projectDir, err := openDeleteDir(projects, project)
	if err != nil {
		return err
	}
	defer projectDir.Close()
	transcriptInfo, err := projectDir.Lstat(transcript)
	if err != nil {
		return err
	}
	if !transcriptInfo.Mode().IsRegular() {
		return errors.New("session transcript is not a regular file")
	}
	if err := checkOtherProjects(projects, project, transcript); err != nil {
		return err
	}
	history, err := openDeleteDir(root, historyDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if history != nil {
		defer history.Close()
		if err := checkDeleteTree(history, s.ID); err != nil {
			return err
		}
	}
	if err := checkDeleteTree(projectDir, s.ID); err != nil {
		return err
	}
	if err := checkDeleteActivity(ctx, root, s.ID, matches); err != nil {
		return fmt.Errorf("cannot delete session: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := projectDir.Lstat(transcript)
	if err != nil || !os.SameFile(current, transcriptInfo) || current.Size() != transcriptInfo.Size() ||
		!current.ModTime().Equal(transcriptInfo.ModTime()) {
		return errors.New("session transcript changed; refresh before deleting")
	}
	// Remove the transcript last so an ancillary failure leaves a visible
	// session that can be retried. RemoveAll tolerates already-removed trees.
	if err := projectDir.RemoveAll(s.ID); err != nil {
		return fmt.Errorf("session files may be partially deleted: %w", err)
	}
	if history != nil {
		if err := history.RemoveAll(s.ID); err != nil {
			return fmt.Errorf("session files may be partially deleted: %w", err)
		}
	}
	if err := projectDir.Remove(transcript); err != nil {
		return fmt.Errorf("session files may be partially deleted: %w", err)
	}
	return nil
}

// Open each directory separately and verify the opened directory is the
// non-symlink entry we checked. Subsequent operations are rooted there.
func openDeleteDir(root *os.Root, name string) (*os.Root, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a regular directory", name)
	}
	dir, err := root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := dir.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		dir.Close()
		return nil, fmt.Errorf("%s changed while checking deletion paths", name)
	}
	return dir, nil
}

func checkOtherProjects(projects *os.Root, project, transcript string) error {
	entries, err := fs.ReadDir(projects.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == project || (!entry.IsDir() && entry.Type()&fs.ModeSymlink == 0) {
			continue
		}
		_, err := projects.Lstat(filepath.Join(entry.Name(), transcript))
		if err == nil {
			return errors.New("more than one project has this session ID")
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("cannot check for duplicate transcripts: %w", err)
		}
	}
	return nil
}

func checkDeleteTree(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a regular directory", info.Name())
	}
	return nil
}

func checkDeleteActivity(ctx context.Context, root *os.Root, id string, matches func(int, string) (bool, error)) error {
	sessions, err := openDeleteDir(root, "sessions")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot verify Claude activity: %w", err)
	}
	defer sessions.Close()
	entries, err := fs.ReadDir(sessions.FS(), ".")
	if err != nil {
		return fmt.Errorf("cannot verify Claude activity: %w", err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("cannot verify Claude activity from %s", entry.Name())
		}
		data, err := sessions.ReadFile(entry.Name())
		if errors.Is(err, fs.ErrNotExist) {
			continue // Claude removed the record when it exited.
		}
		var record sessionFile
		if err != nil || json.Unmarshal(data, &record) != nil || record.SessionID == "" {
			return fmt.Errorf("cannot verify Claude activity from %s", entry.Name())
		}
		if record.SessionID != id {
			continue
		}
		live, err := matches(record.PID, record.ProcStart)
		if err != nil {
			return fmt.Errorf("cannot verify Claude activity: %w", err)
		}
		if live {
			return fmt.Errorf("stop the running Claude process (PID %d) before deleting", record.PID)
		}
	}
	return nil
}

func processMatches(pid int, start string) (bool, error) {
	if pid <= 0 {
		return false, errors.New("invalid Claude process ID")
	}
	err := syscall.Kill(pid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if err != nil || start == "" {
		// A live or inaccessible PID with no verifiable start time is unsafe.
		return true, nil
	}
	actual, err := proc.StartTime(pid)
	if err != nil {
		return false, err
	}
	return actual == start, nil
}
