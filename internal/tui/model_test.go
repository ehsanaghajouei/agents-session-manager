package tui

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ehsanaghajouei/agents-session-manager/internal/config"
	"github.com/ehsanaghajouei/agents-session-manager/internal/host"
	"github.com/ehsanaghajouei/agents-session-manager/internal/idset"
	"github.com/ehsanaghajouei/agents-session-manager/internal/manager"
	"github.com/ehsanaghajouei/agents-session-manager/internal/names"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
	"github.com/ehsanaghajouei/agents-session-manager/internal/tmux"
)

// noTmux behaves like a host without a running tmux server.
type noTmux struct{}

func (noTmux) Name() string  { return "local" }
func (noTmux) IsLocal() bool { return true }
func (noTmux) Run(context.Context, io.Reader, string, ...string) ([]byte, error) {
	return nil, &host.RunError{Cmd: "tmux", Err: errors.New("exit status 1"), Stderr: "no server running on /tmp/tmux-1000/default"}
}
func (noTmux) Interactive(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }

type stubProv struct{ sessions []provider.Session }

func (stubProv) Name() string { return "claude" }
func (p stubProv) Discover(context.Context, host.Executor) ([]provider.Session, error) {
	return p.sessions, nil
}
func (stubProv) Load(context.Context, host.Executor, provider.Session, int) (provider.Transcript, error) {
	return provider.Transcript{Messages: []provider.Message{{Role: provider.RoleUser, Text: "hello"}}}, nil
}
func (stubProv) Running(context.Context, host.Executor) (map[string]provider.Process, error) {
	return map[string]provider.Process{"b": {PID: 42, SessionID: "b", Status: "busy"}}, nil
}
func (stubProv) ResumeCmd(s provider.Session) []string { return []string{"claude", "--resume", s.ID} }
func (stubProv) NewCmd(id string) []string             { return []string{"claude", "--session-id", id} }

func newTestModel(t *testing.T, width, height int) *Model {
	t.Helper()
	sessions := []provider.Session{
		{ID: "a", Host: "local", Title: "Deploy the API", CWD: "/srv/infra", Messages: 2, CostUSD: 0.5, HasCost: true, LastActive: testNow.Add(-2 * time.Hour)},
		{ID: "b", Host: "local", Title: "Fix login bug", CWD: "/srv/web", Messages: 2, CostUSD: 3, HasCost: true, LastActive: testNow.Add(-time.Minute * 5)},
		{ID: "c", Host: "local", Title: "Write docs", CWD: "/srv/web", Messages: 2, LastActive: testNow.Add(-72 * time.Hour)},
	}
	mgr := manager.New(noTmux{}, stubProv{sessions}, tmux.New(noTmux{}, ""), "cc-")
	m := New(Options{Manager: mgr, Config: config.Default()})
	m.now = func() time.Time { return testNow }
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	snap, err := mgr.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m.Update(scanMsg{snap: snap})
	return m
}

func press(m *Model, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		default:
			r := []rune(k)[0]
			msg = tea.KeyPressMsg{Code: r, Text: k}
		}
		m.Update(msg)
	}
}

// titles returns the session titles in list order.
func titles(m *Model) []string {
	var out []string
	for _, r := range m.rows {
		if r.item >= 0 {
			out = append(out, m.items[r.item].Session.Title)
		}
	}
	return out
}

func TestViewFitsTerminal(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {120, 30}, {99, 24}, {60, 12}} {
		m := newTestModel(t, size[0], size[1])
		for _, mode := range []func(){func() {}, func() { press(m, "?") }, func() { press(m, "/", "x") }} {
			mode()
			lines := strings.Split(m.render(), "\n")
			if len(lines) != size[1] {
				t.Errorf("%dx%d: %d lines", size[0], size[1], len(lines))
			}
			for i, l := range lines {
				if w := lipgloss.Width(l); w > size[0] {
					t.Errorf("%dx%d: line %d is %d wide: %q", size[0], size[1], i, w, l)
				}
			}
		}
	}
}

func TestListAndPreview(t *testing.T) {
	m := newTestModel(t, 140, 30)
	if got := strings.Join(titles(m), ","); got != "Fix login bug,Deploy the API,Write docs" {
		t.Errorf("order by activity = %s", got)
	}
	out := m.render()
	// Session b has a process but no tmux session, so it is external.
	for _, want := range []string{"▲ external", "○ saved", "$3.00", "live tmux pane · tab: transcript", "pid 42", "outside tmux"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q", want)
		}
	}

	press(m, "?")
	if help := m.render(); !strings.Contains(help, "C-b d") || !strings.Contains(help, "return to asm") {
		t.Error("help lacks the detach hint")
	}
	press(m, "q") // any key closes help

	if narrow := newTestModel(t, 90, 30).render(); strings.Contains(narrow, "transcript ·") {
		t.Error("preview shown on a narrow terminal")
	}
}

func TestKeys(t *testing.T) {
	m := newTestModel(t, 140, 30)

	press(m, "s") // sort by cost
	if got := strings.Join(titles(m), ","); got != "Fix login bug,Deploy the API,Write docs" {
		t.Errorf("order by cost = %s", got)
	}
	press(m, "s") // by project
	if got := strings.Join(titles(m), ","); got != "Deploy the API,Fix login bug,Write docs" {
		t.Errorf("order by project = %s", got)
	}

	if !strings.Contains(m.render(), "▾ Today (2)") {
		t.Error("not grouped by last by default")
	}
	press(m, "S", "S") // last → none → project
	if !strings.Contains(m.render(), "▾ /srv/web (2)") {
		t.Error("group header missing")
	}
	press(m, "G")
	if it := m.selected(); it == nil || it.Session.Title != "Write docs" {
		t.Errorf("bottom selected %+v", it)
	}

	press(m, "/", "d", "o", "c", "s", "enter")
	if got := strings.Join(titles(m), ","); got != "Write docs" {
		t.Errorf("filtered = %s", got)
	}
	press(m, "esc")
	if len(titles(m)) != 3 {
		t.Error("esc did not clear the filter")
	}

	// x on a session without tmux reports an error instead of a dialog.
	press(m, "x")
	if m.dialog != nil || m.statusLevel != levelError {
		t.Errorf("kill without tmux: dialog=%v status=%q", m.dialog, m.status)
	}
}

func TestExternalResumeAsks(t *testing.T) {
	m := newTestModel(t, 140, 30)
	p := provider.Process{PID: 42, SessionID: "b"}
	m.onResumeReady(resumeReadyMsg{err: &manager.ExternalError{Process: p}, session: provider.Session{ID: "b", Title: "Fix login bug"}})
	if m.dialog == nil || !strings.Contains(m.dialog.body, "pid 42") {
		t.Fatalf("dialog = %+v", m.dialog)
	}
	press(m, "n")
	if m.dialog != nil {
		t.Error("n did not close the dialog")
	}
}

func TestPinKey(t *testing.T) {
	m := newTestModel(t, 140, 30)
	store, err := idset.Load(filepath.Join(t.TempDir(), "pins.json"), "pins")
	if err != nil {
		t.Fatal(err)
	}
	m.pins = store

	press(m, "G") // select the oldest session, "Write docs"
	press(m, "P")
	if !store.Has("local", "c") {
		t.Fatal("P did not pin the selected session")
	}
	out := m.render()
	if !strings.Contains(out, "▾ Pinned (1)") || !strings.Contains(out, strings.Repeat("─", 20)) {
		t.Error("pinned group or divider missing")
	}
	if got := strings.Join(titles(m), ","); got != "Write docs,Fix login bug,Deploy the API" {
		t.Errorf("order = %s", got)
	}
	if it := m.selected(); it == nil || it.Session.ID != "c" {
		t.Error("selection did not follow the pinned session")
	}

	press(m, "P")
	if store.Has("local", "c") || strings.Contains(m.render(), "Pinned") {
		t.Error("P did not unpin")
	}
}

func TestRenameKey(t *testing.T) {
	m := newTestModel(t, 140, 30)
	store, err := names.Load(filepath.Join(t.TempDir(), "names.json"))
	if err != nil {
		t.Fatal(err)
	}
	m.names = store

	press(m, "G") // "Write docs"
	press(m, "R")
	if m.dialog == nil || m.dialog.input == nil || m.dialog.input.Value() != "Write docs" {
		t.Fatalf("rename dialog = %+v", m.dialog)
	}
	press(m, "enter") // unchanged: nothing is stored
	if got := store.Get("local", "c"); got != "" {
		t.Errorf("unchanged rename stored %q", got)
	}

	press(m, "R")
	m.dialog.input.SetValue("  handbook  ")
	press(m, "enter")
	if got := store.Get("local", "c"); got != "handbook" {
		t.Fatalf("stored name = %q", got)
	}
	if out := ansi.Strip(m.render()); !strings.Contains(out, "handbook · Write docs") {
		t.Error("preview header lacks name · title")
	}
	for _, q := range []string{"handbook", "write docs"} {
		m.setQuery(q)
		if it := m.selected(); it == nil || it.Session.ID != "c" || len(m.rows) == 0 {
			t.Errorf("filter %q did not match the renamed session", q)
		}
	}
	m.setQuery("")

	// The list shows the name in place of the title.
	row := ""
	for _, l := range strings.Split(m.render(), "\n") {
		if strings.Contains(l, "/srv/web") && strings.Contains(l, "handbook") {
			row = l
		}
	}
	if row == "" || strings.Contains(row, "Write docs") {
		t.Errorf("list row = %q", row)
	}

	press(m, "R")
	m.dialog.input.SetValue("")
	press(m, "enter")
	if got := store.Get("local", "c"); got != "" || strings.Contains(m.render(), "handbook") {
		t.Errorf("empty input did not remove the name (%q)", got)
	}
}

func TestHideKey(t *testing.T) {
	m := newTestModel(t, 140, 30)
	set, err := idset.Load(filepath.Join(t.TempDir(), "hidden.json"), "hidden")
	if err != nil {
		t.Fatal(err)
	}
	m.hidden = set

	// A running session: the dialog says what gets stopped; n leaves it alone.
	press(m, "g") // "Fix login bug", external pid 42
	press(m, "H")
	if m.dialog == nil || !strings.Contains(m.dialog.body, "SIGTERM") || !strings.Contains(m.dialog.body, "pid 42") {
		t.Fatalf("dialog = %+v", m.dialog)
	}
	press(m, "n")
	if set.Has("local", "b") {
		t.Error("n hid the session")
	}

	press(m, "G") // "Write docs", saved
	press(m, "H")
	if m.dialog == nil || m.dialog.title != "Hide session?" {
		t.Fatalf("dialog = %+v", m.dialog)
	}
	press(m, "y")
	if !set.Has("local", "c") {
		t.Fatal("y did not hide the session")
	}
	if got := strings.Join(titles(m), ","); got != "Fix login bug,Deploy the API" {
		t.Errorf("titles = %s", got)
	}
	if out := ansi.Strip(m.render()); !strings.Contains(out, "2/3 shown · 1 hidden") {
		t.Error("top bar lacks the hidden count")
	}

	press(m, "U")
	if got := len(titles(m)); got != 3 {
		t.Errorf("U shows %d sessions, want 3", got)
	}
	press(m, "G", "H")
	if m.dialog == nil || m.dialog.title != "Unhide session?" {
		t.Fatalf("dialog = %+v", m.dialog)
	}
	press(m, "y")
	if set.Has("local", "c") || strings.Contains(ansi.Strip(m.render()), "1 hidden") {
		t.Error("y did not unhide the session")
	}
}

func TestExportImportDialogs(t *testing.T) {
	m := newTestModel(t, 140, 30)
	m.cfg.BackupDir = "/backups"

	press(m, "G", "E") // "Write docs"
	if m.dialog == nil || m.dialog.title != "Export session" || m.dialog.input.Value() != "/backups/c.tar.gz" {
		t.Fatalf("export dialog = %+v", m.dialog)
	}
	press(m, "esc")

	press(m, "I")
	if m.dialog == nil || m.dialog.title != "Import session" || m.dialog.input.Value() != "/backups/" {
		t.Fatalf("import dialog = %+v", m.dialog)
	}
	press(m, "esc")

	info := provider.BackupInfo{
		Backup:       provider.Backup{SessionID: "c", CWD: "/srv/web", Title: "Write docs", Version: "2.1.300"},
		Exists:       true,
		LocalVersion: "2.1.99",
	}
	m.onInspect(inspectMsg{path: "/backups/c.tar.gz", info: info})
	if m.dialog == nil || m.dialog.input.Value() != "/srv/web" {
		t.Fatalf("project dialog = %+v", m.dialog)
	}
	body := ansi.Strip(m.dialog.body)
	for _, want := range []string{"already exists here", "v2.1.99, older than v2.1.300"} {
		if !strings.Contains(body, want) {
			t.Errorf("dialog lacks %q:\n%s", want, body)
		}
	}
}

func TestOlderVersion(t *testing.T) {
	for _, c := range []struct {
		local, v string
		want     bool
	}{
		{"2.1.99", "2.1.300", true},
		{"2.1.300", "2.1.300", false},
		{"2.2.0", "2.1.300", false},
		{"", "2.1.300", false},
		{"2.1.300", "", false},
		{"2.1.x", "2.1.300", false},
	} {
		if got := olderVersion(c.local, c.v); got != c.want {
			t.Errorf("olderVersion(%q, %q) = %v", c.local, c.v, got)
		}
	}
}
