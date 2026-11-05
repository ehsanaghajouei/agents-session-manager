package tui

import (
	"charm.land/bubbles/v2/help"
	"charm.land/lipgloss/v2"

	"github.com/ehsanaghajouei/agents-session-manager/internal/manager"
)

type styles struct {
	title     lipgloss.Style
	titleInfo lipgloss.Style
	colHeader lipgloss.Style
	group     lipgloss.Style
	selected  lipgloss.Style
	dim       lipgloss.Style
	label     lipgloss.Style
	strong    lipgloss.Style
	errText   lipgloss.Style
	warnText  lipgloss.Style
	divider   lipgloss.Style
	hiddenRow lipgloss.Style
	pane      lipgloss.Style
	user      lipgloss.Style
	tool      lipgloss.Style
	notice    lipgloss.Style
	dialog    lipgloss.Style
	state     map[manager.State]lipgloss.Style
	help      help.Styles
}

func newStyles(dark bool) styles {
	c := lipgloss.LightDark(dark)
	accent := c(lipgloss.Color("#5B4BDB"), lipgloss.Color("#A99BFF"))
	text := c(lipgloss.Color("#1F1F24"), lipgloss.Color("#E6E6EB"))
	muted := c(lipgloss.Color("#6B6B76"), lipgloss.Color("#8A8A99"))
	faint := c(lipgloss.Color("#A0A0AB"), lipgloss.Color("#4E4E5C"))
	selBg := c(lipgloss.Color("#E4E0FF"), lipgloss.Color("#2E2A4F"))
	selFg := c(lipgloss.Color("#1C1640"), lipgloss.Color("#F2F0FF"))
	green := c(lipgloss.Color("#1E8A4C"), lipgloss.Color("#5FD38D"))
	yellow := c(lipgloss.Color("#9A6700"), lipgloss.Color("#E5C07B"))
	red := c(lipgloss.Color("#C4302B"), lipgloss.Color("#F07178"))
	blue := c(lipgloss.Color("#1F6FB2"), lipgloss.Color("#6CB6FF"))

	s := lipgloss.NewStyle
	return styles{
		title:     s().Bold(true).Foreground(accent),
		titleInfo: s().Foreground(muted),
		colHeader: s().Bold(true).Foreground(muted),
		group:     s().Bold(true).Foreground(accent),
		selected:  s().Background(selBg).Foreground(selFg),
		dim:       s().Foreground(muted),
		label:     s().Foreground(muted),
		strong:    s().Bold(true).Foreground(text),
		errText:   s().Foreground(red),
		warnText:  s().Foreground(yellow),
		divider:   s().Foreground(faint),
		hiddenRow: s().Foreground(faint),
		pane:      s().Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(faint).PaddingLeft(1),
		user:      s().Bold(true).Foreground(accent),
		tool:      s().Foreground(muted),
		notice:    s().Italic(true).Foreground(muted),
		dialog:    s().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(1, 2),
		state: map[manager.State]lipgloss.Style{
			manager.StateBusy:     s().Foreground(yellow).Bold(true),
			manager.StateIdle:     s().Foreground(green),
			manager.StateTmux:     s().Foreground(blue),
			manager.StateExternal: s().Foreground(red),
			manager.StateSaved:    s().Foreground(muted),
		},
		help: help.DefaultStyles(dark),
	}
}

// stateLabel is the status column text.
func stateLabel(s manager.State) string {
	switch s {
	case manager.StateBusy:
		return "● busy"
	case manager.StateIdle:
		return "● idle"
	case manager.StateTmux:
		return "◆ tmux"
	case manager.StateExternal:
		return "▲ external"
	}
	return "○ saved"
}
