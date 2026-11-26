package manager

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
	"github.com/ehsanaghajouei/agents-session-manager/internal/tmux"
)

type serviceFixture struct {
	sessions []provider.Session
	active   map[string]bool
	err      error
}

func (p serviceFixture) Discover(context.Context, host.Executor) ([]provider.Session, error) {
	return p.sessions, nil
}

func (p serviceFixture) Active(context.Context, host.Executor) (map[string]bool, error) {
	return p.active, p.err
}

type failingTmux struct{ fakeExec }

func (*failingTmux) Run(context.Context, io.Reader, string, ...string) ([]byte, error) {
	return nil, errors.New("cannot list tmux sessions")
}

func TestScanServiceActivity(t *testing.T) {
	ex := &fakeExec{
		sessions: "oc-ses_busy\noc-ses_managed\noc-ses_external-suffix\ncc-ses_external\n",
	}
	p := serviceFixture{
		sessions: []provider.Session{{ID: "ses_external"}, {ID: "ses_busy"}, {ID: "ses_saved"}, {ID: "ses_managed"}},
		active:   map[string]bool{"ses_external": true, "ses_busy": true, "ses_unlisted": true},
	}
	snap, err := ScanService(t.Context(), ex, p, tmux.New(ex, ""), "oc-")
	if err != nil || len(snap.Warnings) != 0 {
		t.Fatalf("ScanService = %+v, %v", snap, err)
	}
	if len(ex.calls) != 1 || !slices.Equal(ex.calls[0], []string{"tmux", "list-sessions", "-F", "#{session_name}"}) {
		t.Fatalf("unexpected tmux commands: %q", ex.calls)
	}
	var states []State
	for _, it := range snap.Items {
		states = append(states, it.State)
		if it.Process != nil || it.Tmux != nil {
			t.Errorf("unexpected process or tmux assignment: %+v", it)
		}
	}
	if want := []State{StateExternal, StateBusy, StateSaved, StateTmux}; !slices.Equal(states, want) {
		t.Fatalf("states = %v; want %v", states, want)
	}
}

func TestScanServiceUnavailableStatus(t *testing.T) {
	for _, source := range []string{"activity", "tmux"} {
		t.Run(source, func(t *testing.T) {
			p := serviceFixture{
				sessions: []provider.Session{{ID: "ses_external"}},
				active:   map[string]bool{"ses_external": true},
			}
			var ex host.Executor = &fakeExec{}
			if source == "activity" {
				p.err = errors.New("cannot list active sessions")
			} else {
				ex = &failingTmux{}
			}
			snap, err := ScanService(t.Context(), ex, p, tmux.New(ex, ""), "oc-")
			if err != nil || len(snap.Items) != 1 || snap.Items[0].State != StateUnknown ||
				len(snap.Warnings) != 1 || !strings.HasPrefix(snap.Warnings[0], source+":") {
				t.Fatalf("ScanService = %+v, %v; want an unknown row and %s warning", snap, err, source)
			}
		})
	}
}
