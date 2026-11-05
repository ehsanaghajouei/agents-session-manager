// Package claude reads Claude Code sessions from its data directory:
// transcripts in projects/<encoded-cwd>/<id>.jsonl and live processes in
// sessions/<pid>.json. Both formats are internal to Claude Code.
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
	"github.com/ehsanaghajouei/agents-session-manager/internal/index"
	"github.com/ehsanaghajouei/agents-session-manager/internal/proc"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
)

// ErrRemote is returned for hosts whose files cannot be read directly.
// Remote discovery over SSH is not implemented yet.
var ErrRemote = errors.New("claude: remote hosts are not supported yet")

// Provider implements provider.Provider for Claude Code.
type Provider struct {
	dir   string
	bin   string
	args  []string
	cache *index.Store
}

// New returns a provider reading dir (usually ~/.claude). bin and args build
// the resume/new commands. cache may be nil.
func New(dir, bin string, args []string, cache *index.Store) *Provider {
	if cache == nil {
		cache = index.Open("")
	}
	return &Provider{dir: dir, bin: bin, args: args, cache: cache}
}

func (p *Provider) Name() string { return "claude" }

// ProjectsDir and SessionsDir are where Claude Code writes; asm watches them.
func (p *Provider) ProjectsDir() string { return filepath.Join(p.dir, "projects") }
func (p *Provider) SessionsDir() string { return filepath.Join(p.dir, "sessions") }

// Discover lists top-level transcripts. Subagent transcripts live in deeper
// directories and are skipped. Files that fail to parse are reported in the
// returned error alongside the sessions that did parse.
func (p *Provider) Discover(ctx context.Context, ex host.Executor) ([]provider.Session, error) {
	if !ex.IsLocal() {
		return nil, ErrRemote
	}
	paths, err := filepath.Glob(filepath.Join(p.ProjectsDir(), "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}

	prefix := "claude|" + ex.Name() + "|"
	sessions := make([]provider.Session, len(paths))
	errs := make([]error, len(paths))
	keep := make(map[string]bool, len(paths))

	sem := make(chan struct{}, runtime.NumCPU())
	var wg sync.WaitGroup
	for i, path := range paths {
		keep[prefix+path] = true
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				errs[i] = ctx.Err()
				return
			}
			sessions[i], errs[i] = p.summarize(ex.Name(), prefix+path, path)
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	p.cache.Prune(prefix, keep)

	out := sessions[:0]
	var failed []error
	for i, s := range sessions {
		if errs[i] != nil {
			failed = append(failed, errs[i])
			continue
		}
		out = append(out, s)
	}
	if len(failed) > 0 {
		return out, fmt.Errorf("%d transcripts unreadable: %w", len(failed), errors.Join(failed...))
	}
	return out, nil
}

// summarize returns the summary of one transcript, reusing the cache when the
// file is unchanged and parsing only appended bytes when it grew.
func (p *Provider) summarize(hostName, key, path string) (provider.Session, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return provider.Session{}, err
	}
	size, mtime := fi.Size(), fi.ModTime().UnixNano()

	var s provider.Session
	var offset int64
	if e, ok := p.cache.Get(key); ok && json.Unmarshal(e.Data, &s) == nil {
		switch {
		case e.Size == size && e.ModTime == mtime:
			return withFallbacks(s, fi.ModTime()), nil
		case size >= e.Size: // transcripts are append-only
			offset = e.Offset
		default: // shrank: rewritten, start over
			s = provider.Session{}
		}
	}

	s.Provider = p.Name()
	s.Host = hostName
	s.ID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	s.Path = path
	offset, err = parseFrom(path, offset, &s)
	if err != nil {
		return provider.Session{}, err
	}

	data, err := json.Marshal(s)
	if err != nil {
		return provider.Session{}, err
	}
	p.cache.Put(key, index.Entry{Size: size, ModTime: mtime, Offset: offset, Data: data})
	return withFallbacks(s, fi.ModTime()), nil
}

// withFallbacks fills fields a transcript may lack. It is applied after
// caching so the cached state stays a pure fold of the file's records.
func withFallbacks(s provider.Session, mtime time.Time) provider.Session {
	if s.LastActive.IsZero() {
		s.LastActive = mtime
	}
	return s
}

// Load returns the last n preview entries of s.
func (p *Provider) Load(ctx context.Context, ex host.Executor, s provider.Session, n int) (provider.Transcript, error) {
	if !ex.IsLocal() {
		return provider.Transcript{}, ErrRemote
	}
	return loadTail(ctx, s.Path, n)
}

type sessionFile struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	Status    string `json:"status"`
	Name      string `json:"name"`
	ProcStart string `json:"procStart"`
	UpdatedAt int64  `json:"updatedAt"` // Unix ms
}

// Running lists live Claude processes by session ID. A record whose pid is
// gone, or now belongs to another process, is ignored.
func (p *Provider) Running(ctx context.Context, ex host.Executor) (map[string]provider.Process, error) {
	if !ex.IsLocal() {
		return nil, ErrRemote
	}
	paths, err := filepath.Glob(filepath.Join(p.SessionsDir(), "*.json"))
	if err != nil {
		return nil, err
	}
	out := map[string]provider.Process{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue // removed while listing: the process exited
		}
		var f sessionFile
		if json.Unmarshal(data, &f) != nil || f.SessionID == "" || !proc.Alive(f.PID, f.ProcStart) {
			continue
		}
		pr := provider.Process{
			PID:       f.PID,
			StartTime: f.ProcStart,
			SessionID: f.SessionID,
			Status:    f.Status,
			CWD:       f.CWD,
			Name:      f.Name,
			UpdatedAt: time.UnixMilli(f.UpdatedAt),
		}
		if old, ok := out[f.SessionID]; !ok || pr.UpdatedAt.After(old.UpdatedAt) {
			out[f.SessionID] = pr
		}
	}
	return out, nil
}

func (p *Provider) ResumeCmd(s provider.Session) []string {
	return append([]string{p.binary(), "--resume", s.ID}, p.args...)
}

func (p *Provider) NewCmd(id string) []string {
	return append([]string{p.binary(), "--session-id", id}, p.args...)
}

// binary resolves the claude path here, because tmux starts commands with
// its server's environment, whose PATH may differ from ours.
func (p *Provider) binary() string {
	if strings.ContainsRune(p.bin, '/') {
		return p.bin
	}
	if path, err := exec.LookPath(p.bin); err == nil {
		return path
	}
	return p.bin
}
