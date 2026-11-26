package manager

import (
	"context"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
	"github.com/ehsanaghajouei/agents-session-manager/internal/tmux"
)

// ServiceProvider reports agent activity without a client PID.
type ServiceProvider interface {
	Discover(context.Context, host.Executor) ([]provider.Session, error)
	Active(context.Context, host.Executor) (map[string]bool, error)
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
