package claude

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
)

// A backup is a .tar.gz holding, in this order:
//
//	manifest.json       format version and provider.Backup
//	transcript.jsonl    projects/<project>/<id>.jsonl
//	transcript/...      projects/<project>/<id>/ (subagents, tool results)
//	file-history/...    file-history/<id>/ (checkpoints)
const (
	backupFormat   = 1
	manifestName   = "manifest.json"
	transcriptName = "transcript.jsonl"
	transcriptDir  = "transcript"
	historyDir     = "file-history"

	// maxProjectDir is the longest project directory name Claude Code uses
	// as is. Longer names get a hash suffix that asm cannot reproduce.
	maxProjectDir = 200
)

type manifest struct {
	Format int `json:"format"`
	provider.Backup
}

// Export writes the session's transcript, its folder and its file history to
// a .tar.gz at path. A line still being written is left out, so a running
// session exports cleanly.
func (p *Provider) Export(ctx context.Context, ex host.Executor, s provider.Session, path string) error {
	if !ex.IsLocal() {
		return ErrRemote
	}
	m := manifest{Format: backupFormat, Backup: provider.Backup{
		Agent:     p.Name(),
		SessionID: s.ID,
		CWD:       s.CWD,
		Title:     s.DisplayTitle(),
		Version:   s.Version,
		Exported:  time.Now().UTC(),
	}}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	gz := gzip.NewWriter(tmp)
	tw := tar.NewWriter(gz)
	err = writeBackup(ctx, tw, m, s.Path, filepath.Join(p.dir, historyDir, s.ID))
	for _, c := range []io.Closer{tw, gz, tmp} {
		if cerr := c.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func writeBackup(ctx context.Context, tw *tar.Writer, m manifest, transcript, history string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := writeEntry(tw, manifestName, bytes.NewReader(data), int64(len(data))); err != nil {
		return err
	}
	if err := addFile(tw, transcriptName, transcript); err != nil {
		return err
	}
	if err := addTree(ctx, tw, transcriptDir, strings.TrimSuffix(transcript, ".jsonl")); err != nil {
		return err
	}
	return addTree(ctx, tw, historyDir, history)
}

// addFile adds the file at src as name. A .jsonl file is cut after its last
// complete line.
func addFile(tw *tar.Writer, name, src string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	size := fi.Size()
	if strings.HasSuffix(src, ".jsonl") {
		if size, err = completeLines(f, size); err != nil {
			return err
		}
	}
	return writeEntry(tw, name, io.LimitReader(f, size), size)
}

// completeLines returns the length of f up to and including its last newline.
func completeLines(f *os.File, size int64) (int64, error) {
	buf := make([]byte, 64<<10)
	for end := size; end > 0; {
		start := max(end-int64(len(buf)), 0)
		n, err := f.ReadAt(buf[:end-start], start)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if i := bytes.LastIndexByte(buf[:n], '\n'); i >= 0 {
			return start + int64(i) + 1, nil
		}
		end = start
	}
	return 0, nil
}

// addTree adds the regular files under src as name/<relative path>. A missing
// src adds nothing; symlinks are skipped.
func addTree(ctx context.Context, tw *tar.Writer, name, src string) error {
	if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		return addFile(tw, name+"/"+filepath.ToSlash(rel), p)
	})
}

func writeEntry(tw *tar.Writer, name string, r io.Reader, size int64) error {
	hdr := &tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o600, Size: size, ModTime: time.Now()}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := io.Copy(tw, r)
	return err
}

// Inspect reads a backup's manifest and checks it against this host.
func (p *Provider) Inspect(ctx context.Context, ex host.Executor, path string) (provider.BackupInfo, error) {
	if !ex.IsLocal() {
		return provider.BackupInfo{}, ErrRemote
	}
	var m manifest
	err := readBackup(path, func(tr *tar.Reader, mf manifest) error {
		m = mf
		return nil
	})
	if err != nil {
		return provider.BackupInfo{}, err
	}
	existing, err := p.copies(m.SessionID)
	if err != nil {
		return provider.BackupInfo{}, err
	}
	return provider.BackupInfo{Backup: m.Backup, Exists: len(existing) > 0, LocalVersion: p.installedVersion(ctx, ex)}, nil
}

// Import restores the backup at path as a session of the project at cwd. When
// cwd differs from the backup's directory, the transcripts' "cwd" fields move
// to it. Files are unpacked into a staging directory first, so a bad backup
// changes nothing.
func (p *Provider) Import(ctx context.Context, ex host.Executor, path, cwd string, replace bool) error {
	if !ex.IsLocal() {
		return ErrRemote
	}
	if !filepath.IsAbs(cwd) {
		return fmt.Errorf("%q is not an absolute path", cwd)
	}
	cwd = filepath.Clean(cwd)
	project := projectDirName(cwd)
	if len(project) > maxProjectDir {
		return fmt.Errorf("project path is too long: Claude Code shortens directory names over %d characters in a way asm cannot reproduce", maxProjectDir)
	}

	// Staging inside Claude's directory keeps the final moves on one filesystem.
	stage, err := os.MkdirTemp(p.dir, ".asm-import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	var id string
	err = readBackup(path, func(tr *tar.Reader, m manifest) error {
		id = m.SessionID
		return unpack(ctx, tr, stage, cwdRewriter(m.CWD, cwd))
	})
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(stage, transcriptName)); err != nil {
		return fmt.Errorf("%s: the backup has no transcript", path)
	}

	existing, err := p.copies(id)
	if err != nil {
		return err
	}
	if len(existing) > 0 && !replace {
		return provider.ErrSessionExists
	}
	for _, old := range existing {
		if err := os.Remove(old); err != nil {
			return err
		}
		if err := os.RemoveAll(strings.TrimSuffix(old, ".jsonl")); err != nil {
			return err
		}
	}

	dest := filepath.Join(p.ProjectsDir(), project)
	moves := [][2]string{
		{filepath.Join(stage, transcriptDir), filepath.Join(dest, id)},
		{filepath.Join(stage, historyDir), filepath.Join(p.dir, historyDir, id)},
		// Last, so Claude and asm never see a transcript without its files.
		{filepath.Join(stage, transcriptName), filepath.Join(dest, id+".jsonl")},
	}
	for _, mv := range moves {
		if _, err := os.Stat(mv[0]); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		// Whatever is left at the target belongs to no transcript any more.
		if err := os.RemoveAll(mv[1]); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(mv[1]), 0o700); err != nil {
			return err
		}
		if err := os.Rename(mv[0], mv[1]); err != nil {
			return err
		}
	}
	return nil
}

// readBackup opens the backup at path, checks its manifest and passes the
// remaining entries to fn.
func readBackup(path string, fn func(*tar.Reader, manifest) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s is not an asm backup: %w", path, err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	m, err := readManifest(tr)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := fn(tr, m); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// readManifest reads the first entry, which must be a valid manifest.
func readManifest(tr *tar.Reader) (manifest, error) {
	hdr, err := tr.Next()
	if err != nil {
		return manifest{}, fmt.Errorf("not an asm backup: %w", err)
	}
	if hdr.Name != manifestName {
		return manifest{}, errors.New("not an asm backup: no manifest")
	}
	var m manifest
	if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(&m); err != nil {
		return manifest{}, fmt.Errorf("bad manifest: %w", err)
	}
	switch u, err := uuid.Parse(m.SessionID); {
	case m.Format != backupFormat:
		return manifest{}, fmt.Errorf("unsupported backup format %d", m.Format)
	case m.Agent != "claude":
		return manifest{}, fmt.Errorf("the backup is a %q session, not claude", m.Agent)
	case err != nil || u.String() != m.SessionID:
		return manifest{}, fmt.Errorf("bad session ID %q", m.SessionID)
	}
	return m, nil
}

// unpack writes the remaining entries under dir. Only the paths a backup
// contains are accepted, so a crafted archive cannot write anywhere else.
func unpack(ctx context.Context, tr *tar.Reader, dir string, rewrite func([]byte) []byte) error {
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := hdr.Name
		switch {
		case !validEntry(name):
			return fmt.Errorf("unexpected entry %q", name)
		case hdr.Typeflag == tar.TypeDir:
			continue
		case hdr.Typeflag != tar.TypeReg:
			return fmt.Errorf("unexpected entry %q", name)
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		var rw func([]byte) []byte
		if name == transcriptName || strings.HasPrefix(name, transcriptDir+"/") && strings.HasSuffix(name, ".jsonl") {
			rw = rewrite
		}
		if err := writeFile(target, tr, rw); err != nil {
			return err
		}
	}
}

func validEntry(name string) bool {
	if name != path.Clean(name) || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
		return false
	}
	return name == transcriptName || strings.HasPrefix(name, transcriptDir+"/") || strings.HasPrefix(name, historyDir+"/")
}

// writeFile creates target from r, passing each line through rewrite if set.
func writeFile(target string, r io.Reader, rewrite func([]byte) []byte) error {
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	if rewrite == nil {
		_, err = io.Copy(w, r)
	} else {
		err = copyLines(w, r, rewrite)
	}
	if ferr := w.Flush(); err == nil {
		err = ferr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func copyLines(w io.Writer, r io.Reader, rewrite func([]byte) []byte) error {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if _, werr := w.Write(rewrite(line)); werr != nil {
				return werr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// cwdRewriter returns a function that moves "cwd" fields in a JSON line from
// old to new, including subdirectories of old, or nil when nothing moves. It
// edits the text, so the rest of each line stays byte for byte the same.
func cwdRewriter(old, new string) func([]byte) []byte {
	if old == "" || old == new {
		return nil
	}
	const key = `"cwd":"`
	exactFrom, exactTo := []byte(key+jsonEscape(old)+`"`), []byte(key+jsonEscape(new)+`"`)
	subFrom, subTo := []byte(key+jsonEscape(old)+`/`), []byte(key+jsonEscape(new)+`/`)
	return func(line []byte) []byte {
		if !bytes.Contains(line, []byte(key)) {
			return line
		}
		line = bytes.ReplaceAll(line, exactFrom, exactTo)
		return bytes.ReplaceAll(line, subFrom, subTo)
	}
}

// jsonEscape returns s as it appears inside a JSON string written by Claude
// Code, which does not escape HTML characters.
func jsonEscape(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	out := strings.TrimSuffix(b.String(), "\n")
	return out[1 : len(out)-1]
}

// projectDirName is Claude Code's directory name for a project: the path with
// every character other than an ASCII letter or digit replaced by '-'.
func projectDirName(cwd string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, cwd)
}

// copies lists the transcripts of session id in every project directory.
func (p *Provider) copies(id string) ([]string, error) {
	return filepath.Glob(filepath.Join(p.ProjectsDir(), "*", id+".jsonl"))
}

// installedVersion returns the version `claude --version` reports, or "".
func (p *Provider) installedVersion(ctx context.Context, ex host.Executor) string {
	out, err := ex.Run(ctx, nil, p.binary(), "--version")
	if err != nil {
		return ""
	}
	if f := strings.Fields(string(out)); len(f) > 0 {
		return f[0]
	}
	return ""
}
