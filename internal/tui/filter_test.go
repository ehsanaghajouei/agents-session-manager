package tui

import (
	"slices"
	"testing"
	"time"

	"github.com/ehsanaghajouei/agents-session-manager/internal/manager"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
)

var testNow = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func TestParseQuery(t *testing.T) {
	q := parseQuery("deploy status:busy,ext p:Infra since:7d until:2026-11-01 http://x", testNow)
	if !slices.Equal(q.terms, []string{"deploy", "http://x"}) {
		t.Errorf("terms = %q", q.terms)
	}
	if !q.states[manager.StateBusy] || !q.states[manager.StateExternal] || q.states[manager.StateIdle] {
		t.Errorf("states = %v", q.states)
	}
	if q.project != "infra" {
		t.Errorf("project = %q", q.project)
	}
	if !q.since.Equal(testNow.AddDate(0, 0, -7)) {
		t.Errorf("since = %v", q.since)
	}
	if !q.until.Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("until = %v (want the end of Nov 1)", q.until)
	}
	if len(q.errs) != 0 {
		t.Errorf("errs = %v", q.errs)
	}

	if q := parseQuery("status:nope since:yesterday", testNow); len(q.errs) != 2 {
		t.Errorf("errs = %v, want 2", q.errs)
	}
}

func TestFilterItems(t *testing.T) {
	item := func(id, title, cwd string, st manager.State, ago time.Duration) manager.Item {
		return manager.Item{State: st, Session: provider.Session{
			ID: id, Title: title, CWD: cwd, Host: "local", LastActive: testNow.Add(-ago),
		}}
	}
	items := []manager.Item{
		item("a1", "Deploy the API", "/home/u/infra", manager.StateBusy, time.Hour),
		item("b2", "Fix login bug", "/home/u/web", manager.StateSaved, 48*time.Hour),
		item("c3", "Deploy docs", "/home/u/web", manager.StateSaved, 30*24*time.Hour),
	}
	cases := []struct {
		q    string
		want []int
	}{
		{"", []int{0, 1, 2}},
		{"deploy", []int{0, 2}},
		{"dply web", []int{2}}, // fuzzy, and every word must match
		{"status:running", []int{0}},
		{"project:web since:7d", []int{1}},
		{"until:3d", []int{2}},
		{"b2", []int{1}},
	}
	for _, c := range cases {
		if got := filterItems(items, parseQuery(c.q, testNow)); !slices.Equal(got, c.want) {
			t.Errorf("%q: got %v, want %v", c.q, got, c.want)
		}
	}
}
