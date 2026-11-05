package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sahilm/fuzzy"

	"github.com/ehsanaghajouei/agents-session-manager/internal/manager"
)

// query is a parsed filter. Plain words fuzzy-match the title, directory and
// ID; key:value tokens narrow by field.
type query struct {
	raw     string
	terms   []string
	states  map[manager.State]bool
	project string
	host    string
	since   time.Time
	until   time.Time
	errs    []string
}

const filterSyntax = `Filter syntax (words are fuzzy-matched against title, directory and ID):
  status:busy,idle   busy | idle | tmux | external (ext) | saved | running
  project:infra      directory contains "infra"           (alias p:)
  host:local         host contains "local"                (alias h:)
  since:7d           active in the last 30m / 12h / 7d / 2w, or since 2026-10-01
  until:2026-10-01   active up to and including that day, or before 7d ago`

var stateNames = map[string][]manager.State{
	"busy":     {manager.StateBusy},
	"idle":     {manager.StateIdle},
	"tmux":     {manager.StateTmux},
	"external": {manager.StateExternal},
	"ext":      {manager.StateExternal},
	"saved":    {manager.StateSaved},
	"running":  {manager.StateBusy, manager.StateIdle, manager.StateExternal},
}

func parseQuery(s string, now time.Time) query {
	q := query{raw: s}
	for _, f := range strings.Fields(s) {
		k, v, ok := strings.Cut(f, ":")
		if !ok || v == "" {
			q.terms = append(q.terms, f)
			continue
		}
		switch strings.ToLower(k) {
		case "status", "is":
			for _, name := range strings.Split(v, ",") {
				states, ok := stateNames[strings.ToLower(name)]
				if !ok {
					q.errs = append(q.errs, fmt.Sprintf("unknown status %q", name))
					continue
				}
				if q.states == nil {
					q.states = map[manager.State]bool{}
				}
				for _, st := range states {
					q.states[st] = true
				}
			}
		case "project", "p":
			q.project = strings.ToLower(v)
		case "host", "h":
			q.host = strings.ToLower(v)
		case "since":
			t, _, err := parseWhen(v, now)
			if err != nil {
				q.errs = append(q.errs, err.Error())
			}
			q.since = t
		case "until":
			t, isDate, err := parseWhen(v, now)
			if err != nil {
				q.errs = append(q.errs, err.Error())
			}
			if isDate {
				t = t.AddDate(0, 0, 1) // include the whole day
			}
			q.until = t
		default: // e.g. a URL: treat as text
			q.terms = append(q.terms, f)
		}
	}
	return q
}

// parseWhen accepts a date (2006-01-02) or an age such as 30m, 12h, 7d, 2w.
func parseWhen(v string, now time.Time) (t time.Time, isDate bool, err error) {
	if t, err := time.ParseInLocation("2006-01-02", v, now.Location()); err == nil {
		return t, true, nil
	}
	if n, err := strconv.Atoi(v[:len(v)-1]); err == nil && n >= 0 {
		unit := map[byte]time.Duration{'m': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}
		if d, ok := unit[v[len(v)-1]]; ok {
			return now.Add(-time.Duration(n) * d), false, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("bad time %q: use 2006-01-02 or 30m, 12h, 7d, 2w", v)
}

func (q query) matchFields(it manager.Item) bool {
	s := it.Session
	switch {
	case q.states != nil && !q.states[it.State]:
		return false
	case q.project != "" && !strings.Contains(strings.ToLower(s.CWD), q.project):
		return false
	case q.host != "" && !strings.Contains(strings.ToLower(s.Host), q.host):
		return false
	case !q.since.IsZero() && s.LastActive.Before(q.since):
		return false
	case !q.until.IsZero() && !s.LastActive.Before(q.until):
		return false
	}
	return true
}

// filterItems returns the indices of matching items, in input order.
func filterItems(items []manager.Item, q query) []int {
	idx := make([]int, 0, len(items))
	for i, it := range items {
		if q.matchFields(it) {
			idx = append(idx, i)
		}
	}
	// Every word must match on its own.
	for _, term := range q.terms {
		src := make([]string, len(idx))
		for j, i := range idx {
			s := items[i].Session
			src[j] = s.DisplayTitle() + " " + s.CWD + " " + s.ID
		}
		matches := fuzzy.FindNoSort(term, src)
		next := make([]int, 0, len(matches))
		for _, mt := range matches {
			next = append(next, idx[mt.Index])
		}
		idx = next
	}
	return idx
}
