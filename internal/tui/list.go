package tui

import (
	"cmp"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ehsanaghajouei/agents-session-manager/internal/manager"
)

type sortKey int

const (
	sortActivity sortKey = iota
	sortCost
	sortProject
	sortStatus
	numSortKeys
)

func (s sortKey) String() string { return [...]string{"activity", "cost", "project", "status"}[s] }

type groupKey int

const (
	groupNone groupKey = iota
	groupProject
	groupHost
	groupLast
	numGroupKeys
)

func (g groupKey) String() string { return [...]string{"none", "project", "host", "last"}[g] }

// statusRank orders states for sortStatus: attachable sessions first.
var statusRank = map[manager.State]int{
	manager.StateBusy:     0,
	manager.StateIdle:     1,
	manager.StateTmux:     2,
	manager.StateExternal: 3,
	manager.StateSaved:    4,
}

// row is a list line: a session, or (item < 0) a group header or the
// divider below the pinned sessions.
type row struct {
	header  string
	count   int
	item    int
	divider bool
}

// buildRows sorts and groups the items at idx. Pinned items come first in
// their own group, in the same sort order, followed by a divider.
func buildRows(items []manager.Item, idx []int, sk sortKey, gk groupKey, now time.Time, pinned func(manager.Item) bool) []row {
	sorted := slices.Clone(idx)
	slices.SortStableFunc(sorted, func(a, b int) int { return compareItems(items[a], items[b], sk) })

	var top, rest []int
	for _, i := range sorted {
		if pinned != nil && pinned(items[i]) {
			top = append(top, i)
		} else {
			rest = append(rest, i)
		}
	}
	rows := make([]row, 0, len(sorted)+3)
	if len(top) > 0 {
		rows = append(rows, row{header: "Pinned", count: len(top), item: -1})
		for _, i := range top {
			rows = append(rows, row{item: i})
		}
		if len(rest) > 0 {
			rows = append(rows, row{divider: true, item: -1})
		}
	}
	return appendGroups(rows, items, rest, gk, now)
}

func appendGroups(rows []row, items []manager.Item, sorted []int, gk groupKey, now time.Time) []row {
	if gk == groupNone {
		for _, i := range sorted {
			rows = append(rows, row{item: i})
		}
		return rows
	}
	// Groups appear in the order of their first (best-sorted) member, except
	// day groups, which are always newest first.
	var order []string
	groups := map[string][]int{}
	days := map[string]time.Time{}
	for _, i := range sorted {
		var k string
		if gk == groupLast {
			var day time.Time
			k, day = dayGroup(items[i].Session.LastActive, now)
			days[k] = day
		} else {
			k = groupOf(items[i], gk)
		}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], i)
	}
	if gk == groupLast {
		slices.SortStableFunc(order, func(a, b string) int { return days[b].Compare(days[a]) })
	}
	for _, k := range order {
		rows = append(rows, row{header: k, count: len(groups[k]), item: -1})
		for _, i := range groups[k] {
			rows = append(rows, row{item: i})
		}
	}
	return rows
}

func compareItems(a, b manager.Item, sk sortKey) int {
	switch sk {
	case sortCost:
		if c := cmp.Compare(b.Session.CostUSD, a.Session.CostUSD); c != 0 {
			return c
		}
	case sortProject:
		if c := cmp.Compare(a.Session.CWD, b.Session.CWD); c != 0 {
			return c
		}
	case sortStatus:
		if c := cmp.Compare(statusRank[a.State], statusRank[b.State]); c != 0 {
			return c
		}
	}
	if c := b.Session.LastActive.Compare(a.Session.LastActive); c != 0 {
		return c
	}
	return cmp.Compare(a.Session.ID, b.Session.ID)
}

func groupOf(it manager.Item, gk groupKey) string {
	if gk == groupHost {
		return it.Session.Host
	}
	if it.Session.CWD == "" {
		return "(unknown directory)"
	}
	return shortPath(it.Session.CWD)
}

// dayGroup labels t as Today, Yesterday, or its date, and returns the start
// of that day for ordering. Sessions without a time sort last.
func dayGroup(t, now time.Time) (string, time.Time) {
	if t.IsZero() {
		return "Unknown", time.Time{}
	}
	loc := now.Location()
	t = t.In(loc)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	switch {
	case !day.Before(today): // today, or clock skew into the future
		return "Today", today
	case day.Equal(today.AddDate(0, 0, -1)):
		return "Yesterday", day
	}
	return day.Format("2006-01-02 Mon"), day
}

// column is one list column. width 0 marks the flexible title column.
type column struct {
	name     string
	width    int
	right    bool // align right
	keepTail bool // truncate on the left, keeping the end (paths)
	cell     func(it manager.Item, now time.Time) string
}

var (
	colStatus  = column{"STATUS", 10, false, false, func(it manager.Item, _ time.Time) string { return stateLabel(it.State) }}
	colHost    = column{"HOST", 8, false, false, func(it manager.Item, _ time.Time) string { return it.Session.Host }}
	colTitle   = column{"TITLE", 0, false, false, func(it manager.Item, _ time.Time) string { return oneLine(it.Session.DisplayTitle()) }}
	colProject = column{"PROJECT", 24, false, true, func(it manager.Item, _ time.Time) string { return shortPath(it.Session.CWD) }}
	colBranch  = column{"BRANCH", 14, false, false, func(it manager.Item, _ time.Time) string { return it.Session.GitBranch }}
	colLast    = column{"LAST", 6, true, false, func(it manager.Item, now time.Time) string { return relTime(it.Session.LastActive, now) }}
	colCost    = column{"COST", 7, true, false, func(it manager.Item, _ time.Time) string { return cost(it) }}
	colID      = column{"ID", 8, false, false, func(it manager.Item, _ time.Time) string { return shortID(it.Session.ID) }}

	// displayOrder is left to right; keepOrder decides which columns stay
	// when the list is narrow.
	displayOrder = []*column{&colStatus, &colHost, &colTitle, &colProject, &colBranch, &colLast, &colCost, &colID}
	keepOrder    = []*column{&colLast, &colCost, &colProject, &colID, &colBranch, &colHost}
)

const minTitleWidth = 20

// layoutColumns picks the columns that fit in width and sizes the title.
func layoutColumns(width int) []column {
	keep := map[*column]bool{&colStatus: true, &colTitle: true}
	used := colStatus.width + 1 + minTitleWidth
	for _, c := range keepOrder {
		if used+1+c.width <= width {
			keep[c] = true
			used += 1 + c.width
		}
	}
	var cols []column
	fixed := 0
	for _, c := range displayOrder {
		if keep[c] {
			cols = append(cols, *c)
			fixed += c.width
		}
	}
	for i := range cols {
		if cols[i].width == 0 {
			cols[i].width = max(width-fixed-(len(cols)-1), minTitleWidth)
		}
	}
	return cols
}

func fit(s string, width int, right bool) string {
	s = ansi.Truncate(s, width, "…")
	pad := strings.Repeat(" ", max(width-lipgloss.Width(s), 0))
	if right {
		return pad + s
	}
	return s + pad
}

// fitTail is fit for paths: it keeps the end of s.
func fitTail(s string, width int) string {
	if w := lipgloss.Width(s); w > width {
		s = ansi.TruncateLeft(s, w-width+1, "…")
	}
	return fit(s, width, false)
}

func (c column) render(it manager.Item, now time.Time) string {
	if c.keepTail {
		return fitTail(c.cell(it, now), c.width)
	}
	return fit(c.cell(it, now), c.width, c.right)
}

func (m *Model) viewList(width, height int) string {
	cols := layoutColumns(width - 1)
	now := m.now()

	var hdr []string
	for _, c := range cols {
		hdr = append(hdr, fit(c.name, c.width, c.right))
	}
	lines := []string{m.st.colHeader.Render(" " + strings.Join(hdr, " "))}

	if len(m.rows) == 0 {
		msg := "No sessions."
		switch {
		case !m.loaded:
			msg = "Scanning…"
		case m.query.raw != "":
			msg = "No sessions match the filter."
		}
		lines = append(lines, m.st.dim.Render(" "+msg))
	}

	for i := m.offset; i < len(m.rows) && len(lines) < height; i++ {
		r := m.rows[i]
		if r.divider {
			lines = append(lines, m.st.divider.Render(strings.Repeat("─", width)))
			continue
		}
		if r.item < 0 {
			lines = append(lines, m.st.group.Render(fit(fmt.Sprintf(" ▾ %s (%d)", r.header, r.count), width, false)))
			continue
		}
		it := m.items[r.item]
		cells := make([]string, len(cols))
		for j, c := range cols {
			cells[j] = c.render(it, now)
		}
		if i == m.cursor {
			lines = append(lines, m.st.selected.Render(fit(" "+strings.Join(cells, " "), width, false)))
			continue
		}
		for j, c := range cols {
			switch c.name {
			case "STATUS":
				cells[j] = m.st.state[it.State].Render(cells[j])
			case "PROJECT", "BRANCH", "LAST", "COST", "ID", "HOST":
				cells[j] = m.st.dim.Render(cells[j])
			}
		}
		lines = append(lines, " "+strings.Join(cells, " "))
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// --- formatting helpers ---

func relTime(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case t.Year() == now.Year():
		return t.Format("Jan 02")
	}
	return t.Format("2006-01")
}

func cost(it manager.Item) string {
	if !it.Session.HasCost {
		return "—"
	}
	if it.Session.CostUSD >= 100 {
		return fmt.Sprintf("$%.0f", it.Session.CostUSD)
	}
	return fmt.Sprintf("$%.2f", it.Session.CostUSD)
}

func tokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

var home, _ = os.UserHomeDir()

// shortPath replaces the home directory with ~.
func shortPath(p string) string {
	if home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + p[len(home):]
	}
	return p
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
