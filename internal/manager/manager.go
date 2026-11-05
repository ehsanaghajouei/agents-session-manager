// Package manager joins saved sessions, live processes and tmux sessions into
// one list, and runs the actions the TUI offers.
package manager

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
	"github.com/ehsanaghajouei/agents-session-manager/internal/proc"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
	"github.com/ehsanaghajouei/agents-session-manager/internal/tmux"
)

// State is where a session is in its lifecycle.
type State int

const (
	StateSaved    State = iota // not running
	StateTmux                  // its tmux session exists, no Claude process reported yet
	StateIdle                  // running in its tmux session, waiting for input
	StateBusy                  // running in its tmux session, working
	StateExternal              // running, but not inside its tmux session
)

func (s State) String() string {
	return [...]string{"saved", "tmux", "idle", "busy", "external"}[s]
}

// Item is one row of the session list.
type Item struct {
	Session    provider.Session
	Process    *provider.Process // nil when not running
	Tmux       *tmux.Session     // nil when no tmux session exists
	State      State
	DirMissing bool   // the session's working directory is gone
	Alias      string // name the user gave the session in asm; set by the caller, not Scan
}

// Title is the name to show: the alias, else the session's own title.
func (it Item) Title() string {
	if it.Alias != "" {
		return it.Alias
	}
	return it.Session.DisplayTitle()
}

// Snapshot is the result of one scan.
type Snapshot struct {
	Items    []Item
	Warnings []string
}

// ExternalError means a session is already running outside tmux. Resuming it
// would start a second process writing the same transcript.
type ExternalError struct {
	Process provider.Process
}

func (e *ExternalError) Error() string {
	return fmt.Sprintf("session is running outside tmux (pid %d)", e.Process.PID)
}

// Manager drives one provider on one host.
type Manager struct {
	ex     host.Executor
	prov   provider.Provider
	tmux   *tmux.Client
	prefix string

	// Swappable in tests.
	isDescendant func(pid, ancestor int) bool
	dirExists    func(string) bool
}

func New(ex host.Executor, prov provider.Provider, tm *tmux.Client, prefix string) *Manager {
	return &Manager{
		ex:           ex,
		prov:         prov,
		tmux:         tm,
		prefix:       prefix,
		isDescendant: proc.IsDescendant,
		dirExists:    isDir,
	}
}

// TmuxName is the tmux session that hosts a Claude session.
func (m *Manager) TmuxName(id string) string { return m.prefix + id }

// Scan collects the current state. Partial failures become warnings.
func (m *Manager) Scan(ctx context.Context) (Snapshot, error) {
	sessions, err := m.prov.Discover(ctx, m.ex)
	if err != nil && sessions == nil {
		return Snapshot{}, err
	}
	var snap Snapshot
	if err != nil {
		snap.Warnings = append(snap.Warnings, err.Error())
	}
	procs, err := m.prov.Running(ctx, m.ex)
	if err != nil {
		snap.Warnings = append(snap.Warnings, "running sessions: "+err.Error())
	}
	tms, err := m.tmux.Sessions(ctx)
	if err != nil {
		snap.Warnings = append(snap.Warnings, "tmux: "+err.Error())
	}
	snap.Items = m.combine(sessions, procs, tms)
	return snap, nil
}

func (m *Manager) combine(sessions []provider.Session, procs map[string]provider.Process, tms map[string]tmux.Session) []Item {
	seen := make(map[string]bool, len(sessions))
	items := make([]Item, 0, len(sessions))
	add := func(s provider.Session) {
		seen[s.ID] = true
		it := Item{Session: s}
		if p, ok := procs[s.ID]; ok {
			it.Process = &p
		}
		if t, ok := tms[m.TmuxName(s.ID)]; ok {
			it.Tmux = &t
		}
		// A transcript without messages cannot be resumed meaningfully.
		if s.Messages == 0 && it.Process == nil && it.Tmux == nil {
			return
		}
		it.State = m.state(it)
		it.DirMissing = s.CWD != "" && !m.dirExists(s.CWD)
		items = append(items, it)
	}

	for _, s := range sessions {
		add(s)
	}
	// A new session has no transcript until its first message, but its
	// process or tmux session already exists.
	for id, p := range procs {
		if !seen[id] {
			add(provider.Session{
				Provider: m.prov.Name(), Host: m.ex.Name(), ID: id,
				CWD: p.CWD, Name: p.Name, LastActive: p.UpdatedAt,
			})
		}
	}
	for name, t := range tms {
		id, ok := strings.CutPrefix(name, m.prefix)
		if !ok || seen[id] || uuid.Validate(id) != nil {
			continue
		}
		add(provider.Session{
			Provider: m.prov.Name(), Host: m.ex.Name(), ID: id,
			LastActive: t.Created,
		})
	}
	return items
}

func (m *Manager) state(it Item) State {
	switch {
	case it.Process != nil && !m.inTmux(it.Process.PID, it.Tmux):
		return StateExternal
	case it.Process != nil && it.Process.Status == "busy":
		return StateBusy
	case it.Process != nil:
		return StateIdle
	case it.Tmux != nil:
		return StateTmux
	}
	return StateSaved
}

// inTmux reports whether pid runs inside one of the tmux session's panes.
func (m *Manager) inTmux(pid int, t *tmux.Session) bool {
	if t == nil {
		return false
	}
	for _, pane := range t.PanePIDs {
		if m.isDescendant(pid, pane) {
			return true
		}
	}
	return false
}

// Resume prepares s in its tmux session and returns the command that
// attaches to it. State is checked again here because the list can be
// seconds old.
func (m *Manager) Resume(ctx context.Context, s provider.Session) (*exec.Cmd, error) {
	name := m.TmuxName(s.ID)
	tms, err := m.tmux.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	if _, ok := tms[name]; ok {
		return m.tmux.AttachCmd(name), nil
	}
	procs, err := m.prov.Running(ctx, m.ex)
	if err != nil {
		return nil, err
	}
	if p, ok := procs[s.ID]; ok {
		return nil, &ExternalError{Process: p}
	}
	if s.CWD == "" {
		return nil, errors.New("the session's working directory is unknown")
	}
	if !m.dirExists(s.CWD) {
		return nil, fmt.Errorf("directory %s no longer exists", s.CWD)
	}
	if err := m.tmux.NewSession(ctx, name, s.CWD, m.prov.ResumeCmd(s)); err != nil {
		return nil, err
	}
	return m.tmux.AttachCmd(name), nil
}

// New starts a new session in dir and returns its ID and attach command.
func (m *Manager) New(ctx context.Context, dir string) (string, *exec.Cmd, error) {
	dir, err := expandDir(dir)
	if err != nil {
		return "", nil, err
	}
	id := uuid.NewString()
	name := m.TmuxName(id)
	if err := m.tmux.NewSession(ctx, name, dir, m.prov.NewCmd(id)); err != nil {
		return "", nil, err
	}
	return id, m.tmux.AttachCmd(name), nil
}

// Kill ends the session's tmux session and the Claude process inside it.
func (m *Manager) Kill(ctx context.Context, s provider.Session) error {
	return m.tmux.KillSession(ctx, m.TmuxName(s.ID))
}

// Terminate stops a Claude process running outside tmux and waits for it
// to exit.
func (m *Manager) Terminate(p provider.Process) error {
	if !m.ex.IsLocal() {
		return errors.New("terminating remote processes is not supported yet")
	}
	return proc.Terminate(p.PID, p.StartTime, 5*time.Second)
}

// TmuxPrefix is the tmux prefix key, or tmux's default when no server runs.
func (m *Manager) TmuxPrefix(ctx context.Context) string {
	if p, err := m.tmux.Prefix(ctx); err == nil && p != "" {
		return p
	}
	return "C-b"
}

// Capture returns the visible screen of the session's tmux pane.
func (m *Manager) Capture(ctx context.Context, s provider.Session) (string, error) {
	return m.tmux.Capture(ctx, m.TmuxName(s.ID))
}

// Load returns the last n preview entries of s.
func (m *Manager) Load(ctx context.Context, s provider.Session, n int) (provider.Transcript, error) {
	return m.prov.Load(ctx, m.ex, s, n)
}

func (m *Manager) backuper() (provider.Backuper, error) {
	b, ok := m.prov.(provider.Backuper)
	if !ok {
		return nil, fmt.Errorf("%s sessions cannot be exported", m.prov.Name())
	}
	return b, nil
}

// Export writes a backup of s to path. An existing file is replaced only when
// replace is set; otherwise the error wraps fs.ErrExist.
func (m *Manager) Export(ctx context.Context, s provider.Session, path string, replace bool) error {
	b, err := m.backuper()
	if err != nil {
		return err
	}
	if path, err = expandPath(path); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil && !replace {
		return fmt.Errorf("%s: %w", path, fs.ErrExist)
	}
	return b.Export(ctx, m.ex, s, path)
}

// Inspect describes the backup at path.
func (m *Manager) Inspect(ctx context.Context, path string) (provider.BackupInfo, error) {
	b, err := m.backuper()
	if err != nil {
		return provider.BackupInfo{}, err
	}
	if path, err = expandPath(path); err != nil {
		return provider.BackupInfo{}, err
	}
	return b.Inspect(ctx, m.ex, path)
}

// Import restores the backup at path, holding session id, into the project
// at dir. With replace, an existing copy of the session is replaced unless it
// is running; state is checked here because the list can be seconds old.
func (m *Manager) Import(ctx context.Context, path, id, dir string, replace bool) error {
	b, err := m.backuper()
	if err != nil {
		return err
	}
	if path, err = expandPath(path); err != nil {
		return err
	}
	if dir, err = expandDir(dir); err != nil {
		return err
	}
	if replace {
		tms, err := m.tmux.Sessions(ctx)
		if err != nil {
			return err
		}
		procs, err := m.prov.Running(ctx, m.ex)
		if err != nil {
			return err
		}
		if _, ok := tms[m.TmuxName(id)]; ok || procs[id].PID != 0 {
			return errors.New("the session is running on this system; stop it before replacing it")
		}
	}
	return b.Import(ctx, m.ex, path, dir, replace)
}

// ResumeCommand is a shell command that resumes s, for copying.
func (m *Manager) ResumeCommand(s provider.Session) string {
	argv := m.prov.ResumeCmd(s)
	argv[0] = filepath.Base(argv[0])
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = shellQuote(a)
	}
	cmd := strings.Join(quoted, " ")
	if s.CWD == "" {
		return cmd
	}
	return "cd " + shellQuote(s.CWD) + " && " + cmd
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:@%+,", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// expandPath expands a leading ~ and requires an absolute path.
func expandPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, p[1:])
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%q is not an absolute path", p)
	}
	return filepath.Clean(p), nil
}

func expandDir(dir string) (string, error) {
	dir, err := expandPath(dir)
	if err != nil {
		return "", err
	}
	if !isDir(dir) {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	return dir, nil
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
