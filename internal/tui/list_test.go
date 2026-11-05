package tui

import (
	"slices"
	"testing"
	"time"

	"github.com/ehsanaghajouei/agents-session-manager/internal/manager"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
)

func rowLabels(items []manager.Item, rows []row) []string {
	var out []string
	for _, r := range rows {
		switch {
		case r.divider:
			out = append(out, "---")
			continue
		case r.item < 0:
			out = append(out, "# "+r.header)
			continue
		}
		out = append(out, items[r.item].Session.ID)
	}
	return out
}

func TestSortStatus(t *testing.T) {
	mk := func(id string, st manager.State, ago time.Duration) manager.Item {
		return manager.Item{State: st, Session: provider.Session{ID: id, LastActive: testNow.Add(-ago)}}
	}
	items := []manager.Item{
		mk("saved-new", manager.StateSaved, time.Minute),
		mk("ext", manager.StateExternal, time.Hour),
		mk("tmux", manager.StateTmux, time.Hour),
		mk("idle", manager.StateIdle, time.Hour),
		mk("saved-old", manager.StateSaved, 48*time.Hour),
		mk("busy", manager.StateBusy, 72*time.Hour),
	}
	got := rowLabels(items, buildRows(items, []int{0, 1, 2, 3, 4, 5}, sortStatus, groupNone, testNow, nil))
	want := []string{"busy", "idle", "tmux", "ext", "saved-new", "saved-old"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGroupLast(t *testing.T) {
	mk := func(id string, at time.Time, cost float64) manager.Item {
		return manager.Item{Session: provider.Session{ID: id, LastActive: at, CostUSD: cost}}
	}
	items := []manager.Item{
		mk("old", time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC), 9),
		mk("nov3-a", time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC), 1),
		mk("today", testNow.Add(-time.Hour), 0),
		mk("unknown", time.Time{}, 50),
		mk("yesterday", time.Date(2026, 11, 4, 23, 59, 0, 0, time.UTC), 2),
		mk("nov3-b", time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC), 3),
	}
	// Sorting by cost reorders sessions inside a day, never the days.
	got := rowLabels(items, buildRows(items, []int{0, 1, 2, 3, 4, 5}, sortCost, groupLast, testNow, nil))
	want := []string{
		"# Today", "today",
		"# Yesterday", "yesterday",
		"# 2026-11-03 Tue", "nov3-b", "nov3-a",
		"# 2026-10-20 Tue", "old",
		"# Unknown", "unknown",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

func TestPinned(t *testing.T) {
	mk := func(id string, ago time.Duration, cost float64) manager.Item {
		return manager.Item{Session: provider.Session{ID: id, LastActive: testNow.Add(-ago), CostUSD: cost}}
	}
	items := []manager.Item{
		mk("today", time.Hour, 1),
		mk("pin-old", 30*24*time.Hour, 9),
		mk("pin-new", 2*time.Hour, 0),
		mk("yesterday", 24*time.Hour, 2),
	}
	pinned := func(it manager.Item) bool { return it.Session.ID == "pin-old" || it.Session.ID == "pin-new" }
	idx := []int{0, 1, 2, 3}

	got := rowLabels(items, buildRows(items, idx, sortActivity, groupLast, testNow, pinned))
	want := []string{
		"# Pinned", "pin-new", "pin-old", // current sort, removed from their day groups
		"---",
		"# Today", "today",
		"# Yesterday", "yesterday",
	}
	if !slices.Equal(got, want) {
		t.Errorf("group last\n got %v\nwant %v", got, want)
	}

	got = rowLabels(items, buildRows(items, idx, sortCost, groupNone, testNow, pinned))
	want = []string{"# Pinned", "pin-old", "pin-new", "---", "yesterday", "today"}
	if !slices.Equal(got, want) {
		t.Errorf("group none by cost\n got %v\nwant %v", got, want)
	}

	// No divider when everything is pinned.
	all := func(manager.Item) bool { return true }
	if got := rowLabels(items, buildRows(items, idx, sortActivity, groupNone, testNow, all)); slices.Contains(got, "---") {
		t.Errorf("divider with nothing below it: %v", got)
	}
}
