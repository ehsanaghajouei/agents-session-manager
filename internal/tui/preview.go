package tui

import (
	"fmt"
	"strings"
	"sync"

	"charm.land/glamour/v2"
	gstyles "charm.land/glamour/v2/styles"
	"github.com/charmbracelet/x/ansi"

	"github.com/ehsanaghajouei/agents-session-manager/internal/manager"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
)

// headerLines is the fixed height of the preview's metadata block, so the
// transcript viewport keeps one size while the selection changes.
const headerLines = 7

// maxRenderChars bounds one message in the preview; it is a glance, not a reader.
const maxRenderChars = 6000

// mdRenderer caches glamour renderers per width and theme. Rendering is
// serialized because a renderer is not safe for concurrent use.
type mdRenderer struct {
	mu sync.Mutex
	r  map[mdKey]*glamour.TermRenderer
}

type mdKey struct {
	width int
	dark  bool
}

func newMDRenderer() *mdRenderer { return &mdRenderer{r: map[mdKey]*glamour.TermRenderer{}} }

func (c *mdRenderer) render(text string, width int, dark bool) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := mdKey{width, dark}
	r, ok := c.r[k]
	if !ok {
		style := gstyles.LightStyle
		if dark {
			style = gstyles.DarkStyle
		}
		var err error
		r, err = glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(width-4))
		if err != nil {
			return ansi.Wordwrap(text, width, "")
		}
		c.r[k] = r
	}
	out, err := r.Render(text)
	if err != nil {
		return ansi.Wordwrap(text, width, "")
	}
	return strings.Trim(out, "\n")
}

func clip(s string) string {
	if len(s) > maxRenderChars {
		return s[:maxRenderChars] + " …"
	}
	return s
}

func renderTranscript(t provider.Transcript, width int, dark bool, st styles, md *mdRenderer) string {
	if len(t.Messages) == 0 {
		return st.dim.Render("No messages yet.")
	}
	var blocks []string
	for _, msg := range t.Messages {
		switch msg.Role {
		case provider.RoleUser:
			lines := strings.Split(ansi.Wrap(clip(msg.Text), width-2, ""), "\n")
			for i, l := range lines {
				prefix := "  "
				if i == 0 {
					prefix = "❯ "
				}
				lines[i] = st.user.Render(prefix) + st.strong.Render(l)
			}
			blocks = append(blocks, strings.Join(lines, "\n"))
		case provider.RoleAssistant:
			blocks = append(blocks, md.render(clip(msg.Text), width, dark))
		case provider.RoleTool:
			var lines []string
			if more := msg.Count - len(msg.Tools); more > 0 {
				lines = append(lines, fmt.Sprintf("  ⏺ … %d earlier tool calls", more))
			}
			for _, t := range msg.Tools {
				lines = append(lines, "  ⏺ "+t)
			}
			for i, l := range lines {
				lines[i] = st.tool.Render(ansi.Truncate(l, width, "…"))
			}
			blocks = append(blocks, strings.Join(lines, "\n"))
		case provider.RoleNotice:
			blocks = append(blocks, st.notice.Render(ansi.Truncate("  — "+msg.Text+" —", width, "…")))
		}
	}
	return strings.Join(blocks, "\n\n")
}

// previewHeader returns exactly headerLines lines of session metadata.
func (m *Model) previewHeader(it manager.Item, width int) []string {
	s := it.Session
	st := m.st
	label := func(l string) string { return st.label.Render(fmt.Sprintf("%-7s", l)) }
	sep := st.dim.Render(" · ")
	dash := func(v string) string {
		if v == "" {
			return st.dim.Render("—")
		}
		return v
	}

	state := st.state[it.State].Render(it.State.String())
	if p := it.Process; p != nil {
		state += sep + fmt.Sprintf("pid %d", p.PID)
		if it.State == manager.StateExternal {
			state += sep + st.errText.Render("outside tmux")
		}
	}
	if t := it.Tmux; t != nil {
		state += sep + t.Name
		if t.Attached {
			state += " (attached)"
		}
	}
	if s.ContinuedIn != "" {
		state += sep + "continued in " + shortID(s.ContinuedIn)
	}

	dir := dash(shortPath(s.CWD))
	if it.DirMissing {
		dir += " " + st.errText.Render("(missing)")
	}

	version := ""
	if s.Version != "" {
		version = "v" + s.Version
	}

	started := "—"
	if !s.Started.IsZero() {
		started = s.Started.Local().Format("2006-01-02 15:04")
	}
	active := relTime(s.LastActive, m.now())
	if active != "—" && active != "now" {
		active += " ago"
	}

	usage := st.dim.Render("no cost data")
	if s.HasCost {
		usage = cost(it) + sep + tokens(s.Tokens) + " tokens" + sep + fmt.Sprintf("+%d −%d lines", s.LinesAdded, s.LinesRemoved)
	}
	usage += sep + fmt.Sprintf("%d prompts", s.Prompts)

	id := dash(s.ID)
	if s.Name != "" {
		id += sep + s.Name
	}

	lines := []string{
		st.strong.Render(oneLine(s.DisplayTitle())),
		label("State") + state,
		label("Dir") + dir,
		label("Git") + dash(s.GitBranch) + sep + dash(s.Model) + sep + dash(version),
		label("Time") + "started " + started + sep + "active " + active,
		label("Usage") + usage,
		label("ID") + id,
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "…")
	}
	return lines
}

func (m *Model) viewPreview(height int) string {
	w := m.prevW
	var lines []string
	if it := m.selected(); it != nil {
		lines = m.previewHeader(*it, w)
	} else {
		lines = []string{m.st.dim.Render("Nothing selected.")}
	}
	for len(lines) < headerLines {
		lines = append(lines, "")
	}
	label := " transcript · tab: live "
	if m.live {
		label = " live tmux pane · tab: transcript "
	}
	rule := "──" + label + strings.Repeat("─", max(w-ansi.StringWidth(label)-2, 0))
	lines = append(lines, m.st.divider.Render(rule), m.vp.View())
	return m.st.pane.Height(height).MaxHeight(height).Render(strings.Join(lines, "\n"))
}

// liveText formats a tmux capture for the preview.
func liveText(s string) string {
	return strings.TrimRight(s, "\n ")
}

// transcriptKey identifies one rendering of a transcript.
type transcriptKey struct {
	id      string
	version string
	width   int
	dark    bool
}

func (m *Model) transcriptKey(it manager.Item) transcriptKey {
	s := it.Session
	return transcriptKey{
		id:      s.ID,
		version: fmt.Sprintf("%d-%d", s.Messages, s.LastActive.UnixNano()),
		width:   m.prevW,
		dark:    m.dark,
	}
}
