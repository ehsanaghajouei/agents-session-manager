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

type serviceResumeFixture struct {
	dir         string
	dirErr      error
	activityErr error
	active      bool
	calls       []string
}

func (*serviceResumeFixture) ResumeArgs(id string) ([]string, error) {
	return []string{"/test/opencode", "--session", id}, nil
}

func (p *serviceResumeFixture) SessionDirectory(context.Context, host.Executor, string) (string, error) {
	p.calls = append(p.calls, "directory")
	return p.dir, p.dirErr
}

func (p *serviceResumeFixture) Active(context.Context, host.Executor) (map[string]bool, error) {
	p.calls = append(p.calls, "activity")
	return map[string]bool{"ses_saved": p.active}, p.activityErr
}

func TestResumeService(t *testing.T) {
	for _, tt := range []struct {
		name        string
		names       string
		active      bool
		activityErr error
		dirErr      error
		wantErr     string
		wantCalls   []string
		wantNew     bool
	}{
		{name: "reattach", names: "oc-ses_saved\n", active: true},
		{name: "new pane", names: "oc-ses_saved-other\n", wantCalls: []string{"directory", "activity"}, wantNew: true},
		{name: "active", active: true, wantErr: "active outside", wantCalls: []string{"directory", "activity"}},
		{name: "activity unavailable", activityErr: errors.New("unavailable"), wantErr: "cannot verify session activity", wantCalls: []string{"directory", "activity"}},
		{name: "session missing", dirErr: errors.New("session not found"), wantErr: "session not found", wantCalls: []string{"directory"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TMUX", "")
			ex := &fakeExec{sessions: tt.names}
			p := &serviceResumeFixture{dir: t.TempDir(), dirErr: tt.dirErr, active: tt.active, activityErr: tt.activityErr}
			cmd, err := ResumeService(t.Context(), ex, p, tmux.New(ex, ""), "oc-", "ses_saved")
			if tt.wantErr != "" {
				if cmd != nil || err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ResumeService = %v, %v; want %q", cmd, err, tt.wantErr)
				}
			} else if err != nil || cmd == nil || !slices.Equal(cmd.Args, []string{"tmux", "attach-session", "-t", "=oc-ses_saved"}) {
				t.Fatalf("ResumeService = %v, %v; want exact attach", cmd, err)
			}
			if !slices.Equal(p.calls, tt.wantCalls) {
				t.Fatalf("service calls = %q; want %q", p.calls, tt.wantCalls)
			}
			wantTmux := [][]string{{"tmux", "list-sessions", "-F", "#{session_name}"}}
			if tt.wantNew {
				wantTmux = append(wantTmux, []string{"tmux", "new-session", "-d", "-s", "oc-ses_saved", "-c", p.dir, "/test/opencode", "--session", "ses_saved"})
			}
			if !slices.EqualFunc(ex.calls, wantTmux, slices.Equal[[]string]) {
				t.Fatalf("tmux calls = %q; want %q", ex.calls, wantTmux)
			}
		})
	}
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
