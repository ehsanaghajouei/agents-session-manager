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
)

func (m *Model) openCodeDeleteSelection(highlighted *manager.Item) tea.Cmd {
	if len(m.openCodeMarks) == 0 && highlighted == nil {
		return nil
	}
	if m.openCodeDelete == nil {
		return m.setStatus("Delete session is unavailable for OpenCode.", levelInfo)
	}
	if m.pendingActions != 0 {
		return m.setStatus("Wait for the OpenCode action to finish.", levelInfo)
	}
	if len(m.openCodeMarks) == 0 {
		return m.confirmOpenCodeDelete(*highlighted)
	}
	// Use rows, not items: filtering and the show-hidden toggle both affect rows.
	var ids, labels []string
	for _, row := range m.rows {
		if row.item < 0 {
			continue
		}
		item := m.items[row.item]
		id := item.Session.ID
		if m.openCodeMarks[id] {
			ids = append(ids, id)
			labels = append(labels, fmt.Sprintf("%q  %q", id, item.Title()))
		}
	}
	if len(ids) == 0 {
		return m.setStatus("None of the selected OpenCode sessions are visible. Nothing deleted.", levelInfo)
	}
	m.dialog = &dialog{
		title:  fmt.Sprintf("Permanently delete %d OpenCode session(s)?", len(ids)),
		review: &deleteReview{labels: labels},
		onYes: func(m *Model, _ string) tea.Cmd {
			return m.bulkDelete(ids)
		},
	}
	return nil
}

func (m *Model) confirmOpenCodeDelete(it manager.Item) tea.Cmd {
	id := it.Session.ID
	m.dialog = &dialog{
		title: "Delete OpenCode session?",
		body: fmt.Sprintf("%s\n%s\nDeletes its transcript and child sessions too.\n"+
			"This can interrupt active work and cannot be undone.",
			ansi.Truncate(fmt.Sprintf("%q", it.Title()), 54, "…"),
			ansi.Truncate(fmt.Sprintf("ID: %s", id), 54, "…")),
		onYes: func(m *Model, _ string) tea.Cmd {
			remove := m.openCodeDelete
			return m.pendingAction(func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := remove(ctx, id); err != nil {
					return actionMsg{err: fmt.Errorf("Delete failed: %w", err)}
				}
				return actionMsg{text: "Deleted OpenCode session " + shortID(id)}
			})
		},
	}
	return nil
}

type deleteReview struct {
	labels []string
	offset int
}

func (m *Model) deleteReviewPage() int {
	// Reserve two borders and one line each for the title, range and controls.
	return max(m.listH-5, 1)
}

func (m *Model) moveDeleteReview(msg tea.KeyPressMsg, review *deleteReview) bool {
	page := m.deleteReviewPage()
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
	n, page := len(review.labels), m.deleteReviewPage()
	start := min(review.offset, max(n-page, 0))
	end := min(start+page, n)
	width := max(m.width-m.st.dialog.GetHorizontalFrameSize(), 0)
	truncate := func(text string) string { return ansi.Truncate(text, width, "…") }
	parts := []string{
		m.st.title.Render(truncate(d.title)),
		m.st.dim.Render(truncate(fmt.Sprintf("%d–%d of %d selected", start+1, end, n))),
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

type bulkDeleteMsg struct {
	deleted, failed int
	firstError      error
}

func (m *Model) bulkDelete(ids []string) tea.Cmd {
	remove := m.openCodeDelete
	return m.pendingAction(func() tea.Msg {
		var result bulkDeleteMsg
		for _, id := range ids {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := remove(ctx, id)
			cancel()
			if err != nil {
				result.failed++
				if result.firstError == nil {
					result.firstError = fmt.Errorf("%s: %w", id, err)
				}
				continue
			}
			result.deleted++
		}
		return result
	})
}

func (m *Model) onBulkDelete(msg bulkDeleteMsg) tea.Cmd {
	clear(m.openCodeMarks)
	text := fmt.Sprintf("Deleted %d OpenCode session(s).", msg.deleted)
	level := levelInfo
	if msg.failed > 0 {
		text = fmt.Sprintf("Deleted %d; %d unconfirmed. First error: %v", msg.deleted, msg.failed, msg.firstError)
		level = levelError
	}
	return tea.Batch(m.setStatus(text, level), m.scan())
}
