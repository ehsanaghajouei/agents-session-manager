package manager

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
	"github.com/ehsanaghajouei/agents-session-manager/internal/tmux"
)

// ServiceProvider reports agent activity without a client PID.
type ServiceProvider interface {
	Discover(context.Context, host.Executor) ([]provider.Session, error)
	Active(context.Context, host.Executor) (map[string]bool, error)
}

// ServiceResumer opens an existing session through a client of the agent service.
type ServiceResumer interface {
	Active(context.Context, host.Executor) (map[string]bool, error)
	SessionDirectory(context.Context, host.Executor, string) (string, error)
	ResumeArgs(string) ([]string, error)
}

// ServiceCreator creates a saved session and builds the argv to open it.
type ServiceCreator interface {
	Create(context.Context, host.Executor, string) (string, error)
	ResumeArgs(string) ([]string, error)
}

// NewService creates a saved session before opening its tmux pane. If opening
// fails after creation, the session remains saved and its ID is returned.
func NewService(ctx context.Context, ex host.Executor, p ServiceCreator, tm *tmux.Client, prefix, dir string) (string, *exec.Cmd, error) {
	dir, err := expandDir(dir)
	if err != nil {
		return "", nil, err
	}
	if _, err := tm.SessionNames(ctx); err != nil {
		return "", nil, fmt.Errorf("cannot check tmux: %w", err)
	}
	id, err := p.Create(ctx, ex, dir)
	if err != nil {
		return "", nil, err
	}
	openFailed := func(err error) (string, *exec.Cmd, error) {
		return id, nil, fmt.Errorf("session %s was saved but could not open in tmux: %w", id, err)
	}
	argv, err := p.ResumeArgs(id)
	if err != nil {
		return openFailed(err)
	}
	name := prefix + id
	if err := tm.NewSession(ctx, name, dir, argv); err != nil {
		return openFailed(err)
	}
	return id, tm.AttachCmd(name), nil
}

// ResumeService attaches to a managed pane or checks the shared service before
// starting a new client. It never stops a running agent.
func ResumeService(ctx context.Context, ex host.Executor, p ServiceResumer, tm *tmux.Client, prefix, id string) (*exec.Cmd, error) {
	argv, err := p.ResumeArgs(id)
	if err != nil {
		return nil, err
	}
	name := prefix + id
	names, err := tm.SessionNames(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot check tmux: %w", err)
	}
	if names[name] {
		return tm.AttachCmd(name), nil
	}
	dir, err := p.SessionDirectory(ctx, ex, id)
	if err != nil {
		return nil, err
	}
	if !isDir(dir) {
		return nil, fmt.Errorf("directory %s no longer exists", dir)
	}
	active, err := p.Active(ctx, ex)
	if err != nil {
		return nil, fmt.Errorf("cannot verify session activity: %w", err)
	}
	if active[id] {
		return nil, errors.New("the session is active outside its managed tmux session; wait for it to finish")
	}
	if err := tm.NewSession(ctx, name, dir, argv); err != nil {
		return nil, err
	}
	return tm.AttachCmd(name), nil
}

// ScanService combines saved sessions and reported activity with exact tmux
// names. A matching tmux session does not prove where the active request began.
func ScanService(ctx context.Context, ex host.Executor, p ServiceProvider, tm *tmux.Client, prefix string) (Snapshot, error) {
	sessions, err := p.Discover(ctx, ex)
	if err != nil && sessions == nil {
		return Snapshot{}, err
	}
	snap := Snapshot{Items: make([]Item, 0, len(sessions))}
	if err != nil {
		snap.Warnings = append(snap.Warnings, err.Error())
	}
	if len(sessions) == 0 {
		return snap, nil
	}
	active, activityErr := p.Active(ctx, ex)
	names, tmuxErr := tm.SessionNames(ctx)
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if activityErr != nil {
		snap.Warnings = append(snap.Warnings, "activity: "+activityErr.Error())
	}
	if tmuxErr != nil {
		snap.Warnings = append(snap.Warnings, "tmux: "+tmuxErr.Error())
	}
	for _, s := range sessions {
		it := Item{Session: s, DirMissing: s.CWD != "" && !isDir(s.CWD)}
		hasTmux := names[prefix+s.ID]
		switch {
		case activityErr != nil || tmuxErr != nil:
			it.State = StateUnknown
		case active[s.ID] && hasTmux:
			it.State = StateBusy
		case active[s.ID]:
			it.State = StateExternal
		case hasTmux:
			it.State = StateTmux
		default:
			it.State = StateSaved
		}
		snap.Items = append(snap.Items, it)
	}
	return snap, nil
}
