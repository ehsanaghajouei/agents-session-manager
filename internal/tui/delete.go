package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ehsanaghajouei/agents-session-manager/internal/manager"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
)

func (m *Model) deleteAction() func(context.Context, provider.Session) error {
	if m.provider == claudeView {
		return m.claudeDelete
	}
	remove := m.openCodeDelete
	if remove == nil {
		return nil
	}
	return func(ctx context.Context, s provider.Session) error { return remove(ctx, s.ID) }
}

func (m *Model) deleteSelection(highlighted *manager.Item) tea.Cmd {
	if len(m.deleteMarks) == 0 && highlighted == nil {
		return nil
	}
	name := m.provider.String()
	if m.deleteAction() == nil {
		return m.setStatus("Delete session is unavailable for "+name+".", levelInfo)
	}
	if m.pendingActions != 0 {
		return m.setStatus("Wait for the "+name+" action to finish.", levelInfo)
	}
	if len(m.deleteMarks) == 0 {
		return m.confirmDelete(*highlighted)
	}
	// Use rows, not items: filtering and the show-hidden toggle both affect rows.
	var targets []provider.Session
	var labels []string
	seen := make(map[string]bool)
	for _, row := range m.rows {
		if row.item < 0 {
			continue
		}
		item := m.items[row.item]
		id := item.Session.ID
		if m.deleteMarks[id] && !seen[id] {
			seen[id] = true
			targets = append(targets, item.Session)
			labels = append(labels, fmt.Sprintf("%q  %q", id, item.Title()))
		}
	}
	if len(targets) == 0 {
		return m.setStatus("None of the selected "+name+" sessions are visible. Nothing deleted.", levelInfo)
	}
	review := &deleteReview{labels: labels}
	if m.provider == claudeView {
		review.detail = "Deletes transcripts, session files, and checkpoints."
	}
	m.dialog = &dialog{
		title:  fmt.Sprintf("Permanently delete %d %s session(s)?", len(targets), name),
		review: review,
		onYes: func(m *Model, _ string) tea.Cmd {
			return m.deleteSessions(targets, true)
		},
	}
	return nil
}

func (m *Model) confirmDelete(it manager.Item) tea.Cmd {
	detail := "Deletes its transcript and child sessions too.\nThis can interrupt active work and cannot be undone."
	if m.provider == claudeView {
		detail = "Deletes its transcript, session files, and checkpoints.\nThis cannot be undone."
	}
	m.dialog = &dialog{
		title: "Delete " + m.provider.String() + " session?",
		body: fmt.Sprintf("%s\n%s\n%s",
			ansi.Truncate(fmt.Sprintf("%q", it.Title()), 54, "…"),
			ansi.Truncate(fmt.Sprintf("ID: %s", it.Session.ID), 54, "…"), detail),
		onYes: func(m *Model, _ string) tea.Cmd {
			return m.deleteSessions([]provider.Session{it.Session}, false)
		},
	}
	return nil
}

type deleteReview struct {
	labels []string
	detail string
	offset int
}

func (m *Model) deleteReviewPage(review *deleteReview) int {
	// Reserve two borders and one line each for the title, range and controls.
	lines := 5
	if review.detail != "" && m.listH > 6 {
		lines++
	}
	return max(m.listH-lines, 1)
}

func (m *Model) moveDeleteReview(msg tea.KeyPressMsg, review *deleteReview) bool {
	page := m.deleteReviewPage(review)
	last := max(len(review.labels)-page, 0)
	pos := min(review.offset, last)
	k := m.keys
	switch {
	case key.Matches(msg, k.Up):
		pos--
	case key.Matches(msg, k.Down):
		pos++
	case key.Matches(msg, k.PageUp):
		pos -= page
	case key.Matches(msg, k.PageDown):
		pos += page
	case key.Matches(msg, k.Top):
		pos = 0
	case key.Matches(msg, k.Bottom):
		pos = last
	default:
		return false
	}
	review.offset = min(max(pos, 0), last)
	return true
}

func (m *Model) viewDeleteReview(d *dialog) string {
	review := d.review
	n, page := len(review.labels), m.deleteReviewPage(review)
	start := min(review.offset, max(n-page, 0))
	end := min(start+page, n)
	width := max(m.width-m.st.dialog.GetHorizontalFrameSize(), 0)
	truncate := func(text string) string { return ansi.Truncate(text, width, "…") }
	parts := []string{
		m.st.title.Render(truncate(d.title)),
		m.st.dim.Render(truncate(fmt.Sprintf("%d–%d of %d selected", start+1, end, n))),
	}
	if review.detail != "" && m.listH > 6 {
		parts = append(parts, m.st.dim.Render(truncate(review.detail)))
	}
	for _, label := range review.labels[start:end] {
		parts = append(parts, truncate(label))
	}
	controls := "y yes · n no"
	if n > page {
		controls += " · ↑/↓ review"
	}
	parts = append(parts, m.st.dim.Render(truncate(controls)))
	return m.st.dialog.PaddingTop(0).PaddingBottom(0).Render(strings.Join(parts, "\n"))
}

type deleteResultMsg struct {
	bulk            bool
	id              string
	deleted, failed int
	firstError      error
}

func (m *Model) deleteSessions(targets []provider.Session, bulk bool) tea.Cmd {
	remove := m.deleteAction()
	m.deleting = true
	return m.pendingAction(func() tea.Msg {
		result := deleteResultMsg{bulk: bulk, id: targets[0].ID}
		for _, target := range targets {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := remove(ctx, target)
			cancel()
			if err != nil {
				result.failed++
				if result.firstError == nil {
					result.firstError = fmt.Errorf("%s: %w", target.ID, err)
				}
				continue
			}
			result.deleted++
		}
		return result
	})
}

func (m *Model) onDeleteResult(msg deleteResultMsg) tea.Cmd {
	m.deleting = false
	name := m.provider.String()
	if !msg.bulk {
		if msg.firstError != nil {
			status := m.setStatus("Delete failed: "+msg.firstError.Error(), levelError)
			if m.provider == openCodeView {
				return status
			}
			return tea.Batch(status, m.scan())
		}
		return tea.Batch(m.setStatus("Deleted "+name+" session "+shortID(msg.id), levelInfo), m.scan())
	}
	clear(m.deleteMarks)
	text := fmt.Sprintf("Deleted %d %s session(s).", msg.deleted, name)
	level := levelInfo
	if msg.failed > 0 {
		outcome := "unconfirmed"
		if m.provider == claudeView {
			outcome = "blocked or failed"
		}
		text = fmt.Sprintf("Deleted %d; %d %s. First error: %v", msg.deleted, msg.failed, outcome, msg.firstError)
		level = levelError
	}
	return tea.Batch(m.setStatus(text, level), m.scan())
}
