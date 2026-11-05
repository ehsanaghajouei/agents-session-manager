package manager

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
	"github.com/ehsanaghajouei/agents-session-manager/internal/tmux"
)

// fakeExec answers tmux list commands and records every call.
type fakeExec struct {
	sessions, panes string
	calls           [][]string
}

func (*fakeExec) Name() string  { return "local" }
func (*fakeExec) IsLocal() bool { return true }

func (f *fakeExec) Run(_ context.Context, _ io.Reader, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	switch args[0] {
	case "list-sessions":
		return []byte(f.sessions), nil
	case "list-panes":
		return []byte(f.panes), nil
	}
	return nil, nil
}

func (*fakeExec) Interactive(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

type fakeProv struct {
	sessions []provider.Session
	procs    map[string]provider.Process
}

func (*fakeProv) Name() string { return "claude" }
func (p *fakeProv) Discover(context.Context, host.Executor) ([]provider.Session, error) {
	return p.sessions, nil
}
func (*fakeProv) Load(context.Context, host.Executor, provider.Session, int) (provider.Transcript, error) {
	return provider.Transcript{}, nil
}
func (p *fakeProv) Running(context.Context, host.Executor) (map[string]provider.Process, error) {
	return p.procs, nil
}
func (*fakeProv) ResumeCmd(s provider.Session) []string {
	return []string{"/usr/bin/claude", "--resume", s.ID}
}
func (*fakeProv) NewCmd(id string) []string { return []string{"/usr/bin/claude", "--session-id", id} }

const orphanID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

func newTest(t *testing.T) (*Manager, *fakeExec) {
	t.Helper()
	t.Setenv("TMUX", "")
	ex := &fakeExec{
		sessions: "cc-busy\t1\t1793898000\ncc-foreign\t0\t1793898000\ncc-tmuxonly\t0\t1793898000\ncc-" + orphanID + "\t0\t1793898000\ncc-notauuid\t0\t1\nwork\t1\t1",
		panes:    "cc-busy\t100\ncc-foreign\t300\ncc-tmuxonly\t400\ncc-" + orphanID + "\t500\nwork\t600",
	}
	prov := &fakeProv{
		sessions: []provider.Session{
			{ID: "saved", Messages: 3, CWD: "/home/u/proj"},
			{ID: "busy", Messages: 3, CWD: "/home/u/proj"},
			{ID: "external", Messages: 3, CWD: "/home/u/proj"},
			{ID: "foreign", Messages: 3, CWD: "/home/u/proj"},
			{ID: "tmuxonly", Messages: 3, CWD: "/home/u/proj"},
			{ID: "empty", Messages: 0, CWD: "/home/u/proj"},
			{ID: "gone", Messages: 3, CWD: "/deleted"},
		},
		procs: map[string]provider.Process{
			"busy":     {PID: 200, SessionID: "busy", Status: "busy"},
			"external": {PID: 201, SessionID: "external", Status: "idle"},
			"foreign":  {PID: 202, SessionID: "foreign", Status: "idle"},
			"fresh":    {PID: 203, SessionID: "fresh", Status: "idle", CWD: "/home/u/new", Name: "new-1"},
		},
	}
	m := New(ex, prov, tmux.New(ex, ""), "cc-")
	m.isDescendant = func(pid, ancestor int) bool { return pid == 200 && ancestor == 100 }
	m.dirExists = func(d string) bool { return d != "/deleted" }
	return m, ex
}

func TestScanStates(t *testing.T) {
	m, _ := newTest(t)
	snap, err := m.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]State{}
	for _, it := range snap.Items {
		got[it.Session.ID] = it.State
	}
	want := map[string]State{
		"saved":    StateSaved,
		"busy":     StateBusy,
		"external": StateExternal, // no tmux session
		"foreign":  StateExternal, // tmux session exists, but the process is not in it
		"tmuxonly": StateTmux,
		"gone":     StateSaved,
		"fresh":    StateExternal, // process without a transcript yet
		orphanID:   StateTmux,     // our tmux session without a transcript yet
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("states\n got: %v\nwant: %v", got, want)
	}
	for _, it := range snap.Items {
		if it.DirMissing != (it.Session.ID == "gone") {
			t.Errorf("%s: DirMissing = %v", it.Session.ID, it.DirMissing)
		}
		if it.Session.ID == "fresh" && it.Session.CWD != "/home/u/new" {
			t.Errorf("fresh CWD = %q", it.Session.CWD)
		}
		if it.Session.ID == orphanID && !it.Session.LastActive.Equal(time.Unix(1793898000, 0)) {
			t.Errorf("orphan LastActive = %v", it.Session.LastActive)
		}
	}
}

func TestResume(t *testing.T) {
	ctx := context.Background()

	t.Run("attaches to existing tmux session", func(t *testing.T) {
		m, _ := newTest(t)
		cmd, err := m.Resume(ctx, provider.Session{ID: "tmuxonly", CWD: "/home/u/proj"})
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"tmux", "attach-session", "-t", "=cc-tmuxonly"}; !reflect.DeepEqual(cmd.Args, want) {
			t.Errorf("args = %q, want %q", cmd.Args, want)
		}
	})

	t.Run("refuses a session running outside tmux", func(t *testing.T) {
		m, _ := newTest(t)
		_, err := m.Resume(ctx, provider.Session{ID: "external", CWD: "/home/u/proj"})
		var ext *ExternalError
		if !errors.As(err, &ext) || ext.Process.PID != 201 {
			t.Errorf("err = %v, want ExternalError for pid 201", err)
		}
	})

	t.Run("refuses a missing directory", func(t *testing.T) {
		m, ex := newTest(t)
		_, err := m.Resume(ctx, provider.Session{ID: "gone", CWD: "/deleted"})
		if err == nil || !strings.Contains(err.Error(), "no longer exists") {
			t.Errorf("err = %v", err)
		}
		for _, c := range ex.calls {
			if c[1] == "new-session" {
				t.Error("a tmux session was created")
			}
		}
	})

	t.Run("starts a tmux session in the session directory", func(t *testing.T) {
		m, ex := newTest(t)
		cmd, err := m.Resume(ctx, provider.Session{ID: "saved", CWD: "/home/u/proj"})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"tmux", "new-session", "-d", "-s", "cc-saved", "-c", "/home/u/proj", "/usr/bin/claude", "--resume", "saved"}
		if !slices.ContainsFunc(ex.calls, func(c []string) bool { return reflect.DeepEqual(c, want) }) {
			t.Errorf("calls = %q, want one of %q", ex.calls, want)
		}
		if cmd.Args[1] != "attach-session" {
			t.Errorf("attach args = %q", cmd.Args)
		}
	})

	t.Run("switches client inside tmux", func(t *testing.T) {
		m, _ := newTest(t)
		t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
		cmd, err := m.Resume(ctx, provider.Session{ID: "tmuxonly"})
		if err != nil {
			t.Fatal(err)
		}
		if cmd.Args[1] != "switch-client" {
			t.Errorf("args = %q", cmd.Args)
		}
	})
}

func TestResumeCommand(t *testing.T) {
	m, _ := newTest(t)
	got := m.ResumeCommand(provider.Session{ID: "abc", CWD: "/home/u/my proj"})
	if want := "cd '/home/u/my proj' && claude --resume abc"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
