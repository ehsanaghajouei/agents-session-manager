package tui

import "charm.land/bubbles/v2/key"

type keyMap struct {
	Up, Down, PageUp, PageDown, Top, Bottom key.Binding
	Resume, New, Kill, Pin, Rename, Hide    key.Binding
	CopyID, CopyCmd, Export, Import         key.Binding
	Filter, ClearFilter, Sort, Group        key.Binding
	ShowHidden                              key.Binding
	PreviewMode, TogglePreview              key.Binding
	ScrollDown, ScrollUp, HalfDown, HalfUp  key.Binding
	Refresh, Help, Quit                     key.Binding
}

func defaultKeys() keyMap {
	b := func(keys []string, k, desc string) key.Binding {
		return key.NewBinding(key.WithKeys(keys...), key.WithHelp(k, desc))
	}
	return keyMap{
		Up:            b([]string{"up", "k"}, "↑/k", "up"),
		Down:          b([]string{"down", "j"}, "↓/j", "down"),
		PageUp:        b([]string{"pgup", "ctrl+b"}, "pgup", "page up"),
		PageDown:      b([]string{"pgdown", "ctrl+f"}, "pgdn", "page down"),
		Top:           b([]string{"home", "g"}, "g", "top"),
		Bottom:        b([]string{"end", "G"}, "G", "bottom"),
		Resume:        b([]string{"enter"}, "enter", "resume/attach"),
		New:           b([]string{"n"}, "n", "new session"),
		Kill:          b([]string{"x"}, "x", "kill tmux session"),
		Pin:           b([]string{"P"}, "P", "pin/unpin"),
		Rename:        b([]string{"R"}, "R", "rename"),
		Hide:          b([]string{"H"}, "H", "hide/unhide"),
		CopyID:        b([]string{"y"}, "y", "copy session ID"),
		CopyCmd:       b([]string{"Y"}, "Y", "copy resume command"),
		Export:        b([]string{"E"}, "E", "export to a backup file"),
		Import:        b([]string{"I"}, "I", "import a backup file"),
		Filter:        b([]string{"/"}, "/", "filter"),
		ClearFilter:   b([]string{"esc"}, "esc", "clear filter"),
		Sort:          b([]string{"s"}, "s", "sort"),
		Group:         b([]string{"S"}, "S", "group"),
		ShowHidden:    b([]string{"U"}, "U", "show hidden"),
		PreviewMode:   b([]string{"tab"}, "tab", "transcript/live"),
		TogglePreview: b([]string{"p"}, "p", "show/hide preview"),
		ScrollDown:    b([]string{"J"}, "J", "preview down"),
		ScrollUp:      b([]string{"K"}, "K", "preview up"),
		HalfDown:      b([]string{"ctrl+d"}, "ctrl+d", "preview half page down"),
		HalfUp:        b([]string{"ctrl+u"}, "ctrl+u", "preview half page up"),
		Refresh:       b([]string{"r"}, "r", "refresh"),
		Help:          b([]string{"?"}, "?", "help"),
		Quit:          b([]string{"q", "ctrl+c"}, "q", "quit"),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Resume, k.New, k.Filter, k.Sort, k.Group, k.PreviewMode, k.Help, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown, k.Top, k.Bottom},
		{k.Resume, k.New, k.Kill, k.Pin, k.Rename, k.Hide, k.Export, k.Import, k.CopyID, k.CopyCmd, k.Refresh},
		{k.Filter, k.ClearFilter, k.Sort, k.Group, k.ShowHidden},
		{k.PreviewMode, k.TogglePreview, k.ScrollDown, k.ScrollUp, k.HalfDown, k.HalfUp},
		{k.Help, k.Quit},
	}
}
