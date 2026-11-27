package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ehsanaghajouei/agents-session-manager/internal/manager"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
)

var spacePress = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}

func TestClaudeDeleteSelection(t *testing.T) {
	m := newTestModel(t, 100, 24)
	var calls []string
	m.claudeDelete = func(_ context.Context, s provider.Session) error {
		calls = append(calls, s.ID)
		if s.ID == "b" {
			return errors.New("stop the running Claude process")
		}
		return nil
	}
	if k := m.helpKeys(); !k.Mark.Enabled() || !k.Delete.Enabled() {
		t.Fatal("deletion bindings are missing from Claude Code help")
	}
	press(m, "D")
	if m.dialog == nil || !strings.Contains(m.dialog.body, "checkpoints") || !strings.Contains(m.dialog.body, "cannot be undone") {
		t.Fatal("single deletion did not describe its permanent effect")
	}
	press(m, "n")
	if len(calls) != 0 || m.dialog != nil {
		t.Fatal("cancelling started deletion")
	}
	for range 3 {
		m.Update(spacePress)
		press(m, "j")
	}
	m.setQuery("nonexistent")
	press(m, "D")
	if m.dialog != nil || !strings.Contains(m.status, "Nothing deleted") {
		t.Fatal("all-filtered marks did not block deletion")
	}
	m.setQuery("project:web")
	press(m, "D")
	if m.dialog == nil || m.dialog.review == nil || len(m.dialog.review.labels) != 2 {
		t.Fatal("bulk confirmation did not contain exactly the visible marked sessions")
	}
	_, run := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if run == nil || len(calls) != 0 {
		t.Fatal("confirmation did not queue deletion")
	}
	press(m, "enter", "n", "I")
	if m.dialog != nil || m.pendingActions != 1 {
		t.Fatal("another session action started during deletion")
	}
	_, refresh := m.Update(run())
	if !slices.Equal(calls, []string{"b", "c"}) || refresh == nil || len(m.deleteMarks) != 0 ||
		!strings.Contains(m.status, "Deleted 1; 1 blocked or failed") {
		t.Fatalf("Claude bulk deletion: calls=%v, status=%q, marks=%v", calls, m.status, m.deleteMarks)
	}
}

func TestOpenCodeBulkDelete(t *testing.T) {
	m := newTestModel(t, 100, 24)
	items := []manager.Item{
		{Session: provider.Session{ID: "ses_a", Title: "match alpha"}},
		{Session: provider.Session{ID: "ses_b", Title: "match beta"}},
		{Session: provider.Session{ID: "ses_c", Title: "excluded gamma"}},
		{Session: provider.Session{ID: "ses_d", Title: "unmarked delta"}},
	}
	m.openCodeScan = func(context.Context) (manager.Snapshot, error) {
		return manager.Snapshot{Items: slices.Clone(items)}, nil
	}
	var calls []string
	m.openCodeDelete = func(_ context.Context, id string) error {
		calls = append(calls, id)
		if id == "ses_a" {
			return errors.New("service refused")
		}
		items = slices.DeleteFunc(items, func(it manager.Item) bool { return it.Session.ID == id })
		return nil
	}
	m.Update(spacePress)
	if len(m.deleteMarks) != 0 || m.helpKeys().Mark.Enabled() {
		t.Fatal("bulk selection is available without a deletion callback")
	}
	_, scan := m.Update(tea.KeyPressMsg{Code: 'v', Text: "v"})
	m.Update(scan())
	if !m.helpKeys().Mark.Enabled() {
		t.Fatal("selection binding is missing from OpenCode help")
	}
	m.Update(spacePress)
	m.Update(spacePress)
	if len(m.deleteMarks) != 0 {
		t.Fatal("pressing Space again did not unmark the session")
	}
	for range 3 {
		m.Update(spacePress)
		press(m, "j")
	}
	if !strings.Contains(ansi.Strip(m.viewList(m.listW, m.listH)), "\n*") || len(m.deleteMarks) != 3 {
		t.Fatalf("marked rows not visible or incorrect marks: %v", m.deleteMarks)
	}

	m.setQuery("unmarked")
	press(m, "D")
	if m.dialog != nil || !strings.Contains(m.status, "Nothing deleted") || len(calls) != 0 {
		t.Fatal("all-filtered marks fell back to deleting the highlighted session")
	}
	m.setQuery("nonexistent")
	press(m, "D")
	if m.selected() != nil || m.dialog != nil || !strings.Contains(m.status, "Nothing deleted") {
		t.Fatal("all-filtered marks gave no explanation when the list was empty")
	}
	m.setQuery("match")
	press(m, "D")
	if m.dialog == nil || m.dialog.review == nil {
		t.Fatal("bulk delete did not ask for confirmation")
	}
	dialog := ansi.Strip(m.viewDialog())
	for _, want := range []string{"2 OpenCode", "ses_a", "ses_b"} {
		if !strings.Contains(dialog, want) {
			t.Errorf("review lacks %q: %q", want, dialog)
		}
	}
	for _, unwanted := range []string{"ses_c", "ses_d", "child"} {
		if strings.Contains(dialog, unwanted) {
			t.Errorf("review unexpectedly contains %q: %q", unwanted, dialog)
		}
	}
	press(m, "n")
	if m.dialog != nil || len(m.deleteMarks) != 3 || len(calls) != 0 {
		t.Fatal("cancelling did not preserve the marks without deleting")
	}
	press(m, "D")
	_, run := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	press(m, "D", "v")
	if run == nil || m.dialog != nil || m.pendingActions != 1 || m.provider != openCodeView || len(calls) != 0 {
		t.Fatal("confirmation did not queue exactly one operation")
	}
	_, done := m.Update(run())
	if !slices.Equal(calls, []string{"ses_a", "ses_b"}) {
		t.Fatalf("attempted deletes = %v", calls)
	}
	if len(m.deleteMarks) != 0 || m.pendingActions != 0 || m.statusLevel != levelError ||
		!strings.Contains(m.status, "Deleted 1; 1 unconfirmed") || !strings.Contains(m.status, "service refused") {
		t.Fatalf("partial failure did not clear marks and report the outcome: %q", m.status)
	}
	m.Update(done().(tea.BatchMsg)[1]())
	if got := titles(m); !slices.Equal(got, []string{"match alpha"}) {
		t.Fatalf("post-delete rows = %v", got)
	}

	m.Update(spacePress)
	_, back := m.Update(tea.KeyPressMsg{Code: 'v', Text: "v"})
	m.Update(back())
	if len(m.deleteMarks) != 0 || m.provider != claudeView {
		t.Fatal("switching provider did not clear the marks")
	}
}

func TestOpenCodeBulkDeleteReviewScrolls(t *testing.T) {
	m := newTestModel(t, 60, 12)
	m.provider = openCodeView
	m.openCodeDelete = func(context.Context, string) error { return nil }
	m.items = nil
	for i := range 20 {
		id := fmt.Sprintf("ses_%02d", i)
		m.items = append(m.items, manager.Item{Session: provider.Session{ID: id, Title: strings.Repeat("long title ", 10)}})
		m.deleteMarks[id] = true
	}
	m.rebuild()
	press(m, "D")
	if start := ansi.Strip(m.viewDialog()); !strings.Contains(start, "ses_00") || strings.Contains(start, "ses_19") {
		t.Fatalf("unexpected first page: %q", start)
	}
	press(m, "G")
	out := ansi.Strip(m.render())
	if !strings.Contains(out, "ses_19") || strings.Contains(out, "ses_00") {
		t.Fatalf("unexpected last page: %q", out)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("review is %d lines tall, expected %d", len(lines), m.height)
	}
	for _, line := range lines {
		if lipgloss.Width(line) > m.width {
			t.Fatalf("review exceeds terminal width: %q", line)
		}
	}
}
