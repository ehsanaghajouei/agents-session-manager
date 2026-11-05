// Package tui is the Bubble Tea interface: a session list, a preview pane,
// and dialogs for actions.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ehsanaghajouei/agents-session-manager/internal/config"
	"github.com/ehsanaghajouei/agents-session-manager/internal/idset"
	"github.com/ehsanaghajouei/agents-session-manager/internal/manager"
	"github.com/ehsanaghajouei/agents-session-manager/internal/names"
	"github.com/ehsanaghajouei/agents-session-manager/internal/provider"
)

// Options configures the TUI.
type Options struct {
	Manager *manager.Manager
	Config  config.Config
	// Changes signals that Claude's files changed. Nil disables watching.
	Changes <-chan struct{}
	// AfterScan runs in the scan goroutine, e.g. to persist the cache.
	AfterScan func() error
	// Warnings are shown once at startup.
	Warnings []string
	// Pins holds pinned sessions. Nil disables pinning.
	Pins *idset.Set
	// Names holds the names given to sessions. Nil disables renaming.
	Names *names.Store
	// Hidden holds hidden sessions. Nil disables hiding.
	Hidden *idset.Set
}

type statusLevel int

const (
	levelInfo statusLevel = iota
	levelWarn
	levelError
)

// dialog is a modal: a yes/no confirmation, or a one-line input.
type dialog struct {
	title string
	body  string
	input *textinput.Model // nil for a confirmation
	onYes func(m *Model, value string) tea.Cmd
}

type Model struct {
	mgr       *manager.Manager
	cfg       config.Config
	changes   <-chan struct{}
	afterScan func() error
	startup   []string
	pins      *idset.Set
	names     *names.Store
	hidden    *idset.Set
	keys      keyMap
	help      help.Model
	md        *mdRenderer
	now       func() time.Time

	width, height int
	listW, listH  int
	prevW         int
	dark          bool
	st            styles

	items      []manager.Item
	warnings   string
	rows       []row
	cursor     int
	offset     int
	selID      string
	sortBy     sortKey
	groupBy    groupKey
	query      query
	filterIn   textinput.Model
	filtering  bool
	showHidden bool // list hidden sessions too, dimmed

	previewOn   bool
	live        bool // preview shows the tmux pane instead of the transcript
	vp          viewport.Model
	previewID   string
	previewSeq  int
	transcripts map[transcriptKey]string
	loading     map[transcriptKey]bool
	captured    map[string]string
	capturing   bool

	dialog     *dialog
	showHelp   bool
	tmuxPrefix string // shown in help as the key that returns to asm

	status      string
	statusLevel statusLevel
	statusSeq   int

	scanning, rescan, loaded bool
}

// New builds the model.
func New(o Options) *Model {
	fi := textinput.New()
	fi.Prompt = " / "
	fi.Placeholder = "words, status:busy, project:name, since:7d"
	m := &Model{
		mgr:         o.Manager,
		cfg:         o.Config,
		changes:     o.Changes,
		afterScan:   o.AfterScan,
		startup:     o.Warnings,
		pins:        o.Pins,
		names:       o.Names,
		hidden:      o.Hidden,
		keys:        defaultKeys(),
		help:        help.New(),
		md:          newMDRenderer(),
		now:         time.Now,
		dark:        true,
		sortBy:      sortActivity,
		groupBy:     groupLast,
		filterIn:    fi,
		previewOn:   true,
		live:        true,
		tmuxPrefix:  "C-b",
		vp:          viewport.New(),
		transcripts: map[transcriptKey]string{},
		loading:     map[transcriptKey]bool{},
		captured:    map[string]string{},
	}
	m.applyTheme(true)
	return m
}

func (m *Model) applyTheme(dark bool) {
	m.dark = dark
	m.st = newStyles(dark)
	m.help.Styles = m.st.help
	m.filterIn.SetStyles(textinput.DefaultStyles(dark))
}

// --- messages ---

type (
	scanMsg struct {
		snap    manager.Snapshot
		err     error
		saveErr error
	}
	tickMsg       struct{}
	changesMsg    struct{}
	previewDueMsg struct{ seq int }
	transcriptMsg struct {
		key     transcriptKey
		content string
	}
	captureTickMsg struct{}
	captureMsg     struct {
		id, text string
		err      error
	}
	// resumeReadyMsg carries the attach command once a tmux session exists.
	resumeReadyMsg struct {
		cmd     *exec.Cmd
		err     error
		session provider.Session
	}
	execDoneMsg struct {
		err     error
		session provider.Session
	}
	actionMsg struct {
		text string
		err  error
	}
	exportMsg struct {
		session provider.Session
		path    string
		err     error
	}
	inspectMsg struct {
		path string
		info provider.BackupInfo
		err  error
	}
	importMsg struct {
		id  string
		err error
	}
	// hideMsg hides or unhides item once anything running in it is stopped.
	hideMsg struct {
		item    manager.Item
		hide    bool
		stopped bool
		err     error
	}
	clearStatusMsg struct{ seq int }
	prefixMsg      struct{ key string }
)

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.scan(), m.tick(), captureTick(), tea.RequestBackgroundColor}
	if m.changes != nil {
		cmds = append(cmds, waitChanges(m.changes))
	}
	if len(m.startup) > 0 {
		cmds = append(cmds, m.setStatus(strings.Join(m.startup, "; "), levelWarn))
	}
	return tea.Batch(cmds...)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, m.syncPreview(true)

	case tea.BackgroundColorMsg:
		m.applyTheme(msg.IsDark())
		return m, m.syncPreview(true)

	case scanMsg:
		return m, m.onScan(msg)

	case tickMsg:
		return m, tea.Batch(m.tick(), m.scan())

	case changesMsg:
		return m, tea.Batch(waitChanges(m.changes), m.scan())

	case previewDueMsg:
		if msg.seq != m.previewSeq {
			return m, nil
		}
		return m, m.syncPreview(true)

	case transcriptMsg:
		delete(m.loading, msg.key)
		if len(m.transcripts) > 64 {
			clear(m.transcripts)
		}
		m.transcripts[msg.key] = msg.content
		if msg.key.id == m.previewID {
			return m, m.syncPreview(false)
		}
		return m, nil

	case captureTickMsg:
		var cmd tea.Cmd
		if it := m.selected(); m.live && m.previewVisible() && it != nil && it.Tmux != nil {
			cmd = m.capture(*it)
		}
		return m, tea.Batch(captureTick(), cmd)

	case captureMsg:
		m.capturing = false
		text := liveText(msg.text)
		if msg.err != nil {
			text = m.st.errText.Render("capture failed: " + msg.err.Error())
		}
		m.captured[msg.id] = text
		if m.live && msg.id == m.previewID {
			return m, m.syncPreview(false)
		}
		return m, nil

	case resumeReadyMsg:
		return m, m.onResumeReady(msg)

	case execDoneMsg:
		var cmd tea.Cmd
		if msg.err != nil {
			cmd = m.setStatus(fmt.Sprintf("tmux: %v. If Claude exited right away, run it directly to see why: %s",
				msg.err, m.mgr.ResumeCommand(msg.session)), levelError)
		}
		return m, tea.Batch(cmd, m.scan())

	case actionMsg:
		if msg.err != nil {
			return m, m.setStatus(msg.err.Error(), levelError)
		}
		return m, tea.Batch(m.setStatus(msg.text, levelInfo), m.scan())

	case hideMsg:
		return m, m.onHide(msg)

	case exportMsg:
		return m, m.onExport(msg)

	case inspectMsg:
		return m, m.onInspect(msg)

	case importMsg:
		return m, m.onImport(msg)

	case clearStatusMsg:
		if msg.seq == m.statusSeq {
			m.status = ""
		}
		return m, nil

	case prefixMsg:
		m.tmuxPrefix = msg.key
		return m, nil

	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	}

	// Other messages (e.g. cursor blink) go to the focused input.
	var cmd tea.Cmd
	switch {
	case m.dialog != nil && m.dialog.input != nil:
		*m.dialog.input, cmd = m.dialog.input.Update(msg)
	case m.filtering:
		m.filterIn, cmd = m.filterIn.Update(msg)
	}
	return m, cmd
}

// --- scanning ---

func (m *Model) scan() tea.Cmd {
	if m.scanning {
		m.rescan = true
		return nil
	}
	m.scanning = true
	mgr, after := m.mgr, m.afterScan
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		snap, err := mgr.Scan(ctx)
		var saveErr error
		if after != nil {
			saveErr = after()
		}
		return scanMsg{snap: snap, err: err, saveErr: saveErr}
	}
}

func (m *Model) onScan(msg scanMsg) tea.Cmd {
	m.scanning = false
	var cmds []tea.Cmd
	if msg.err != nil {
		cmds = append(cmds, m.setStatus("scan failed: "+msg.err.Error(), levelError))
	} else {
		m.items = msg.snap.Items
		m.applyNames()
		m.loaded = true
		warnings := msg.snap.Warnings
		if msg.saveErr != nil {
			warnings = append(warnings, "cache: "+msg.saveErr.Error())
		}
		// Repeat a warning only when it changes, not on every scan.
		if w := strings.Join(warnings, "; "); w != m.warnings {
			m.warnings = w
			if w != "" {
				cmds = append(cmds, m.setStatus(w, levelWarn))
			}
		}
		m.rebuild()
		cmds = append(cmds, m.syncPreview(true))
	}
	if m.rescan {
		m.rescan = false
		cmds = append(cmds, m.scan())
	}
	return tea.Batch(cmds...)
}

func (m *Model) tick() tea.Cmd {
	return tea.Tick(m.cfg.RefreshInterval.Duration, func(time.Time) tea.Msg { return tickMsg{} })
}

func captureTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return captureTickMsg{} })
}

func waitChanges(ch <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		if _, ok := <-ch; !ok {
			return nil
		}
		return changesMsg{}
	}
}

// --- list state ---

// rebuild applies the filter, sort and grouping, keeping the selection.
func (m *Model) rebuild() {
	idx := filterItems(m.items, m.query)
	if !m.showHidden {
		idx = slices.DeleteFunc(idx, func(i int) bool { return m.isHidden(m.items[i]) })
	}
	m.rows = buildRows(m.items, idx, m.sortBy, m.groupBy, m.now(), m.isPinned)
	m.cursor = -1
	for i, r := range m.rows {
		if r.item >= 0 && m.items[r.item].Session.ID == m.selID {
			m.cursor = i
			break
		}
	}
	if m.cursor < 0 {
		m.cursor = max(m.nextSelectable(0, 1), 0)
	}
	if it := m.selected(); it != nil {
		m.selID = it.Session.ID
	}
	m.ensureVisible()
}

func (m *Model) isPinned(it manager.Item) bool {
	return m.pins != nil && m.pins.Has(it.Session.Host, it.Session.ID)
}

func (m *Model) togglePin(it manager.Item) tea.Cmd {
	if m.pins == nil {
		return m.setStatus("Pinning is unavailable: the pins file could not be read at startup.", levelError)
	}
	pinned, err := m.pins.Toggle(it.Session.Host, it.Session.ID)
	if err != nil {
		return m.setStatus("pins: "+err.Error(), levelError)
	}
	m.rebuild()
	if pinned {
		return m.setStatus("Pinned "+shortID(it.Session.ID), levelInfo)
	}
	return m.setStatus("Unpinned "+shortID(it.Session.ID), levelInfo)
}

func (m *Model) isHidden(it manager.Item) bool {
	return m.hidden != nil && m.hidden.Has(it.Session.Host, it.Session.ID)
}

func (m *Model) confirmHide(it manager.Item) tea.Cmd {
	if m.hidden == nil {
		return m.setStatus("Hiding is unavailable: the hidden file could not be read at startup.", levelError)
	}
	title := oneLine(it.Title())
	if m.isHidden(it) {
		m.dialog = &dialog{
			title: "Unhide session?",
			body:  fmt.Sprintf("Show %q in the list again?", title),
			onYes: func(m *Model, _ string) tea.Cmd { return m.onHide(hideMsg{item: it}) },
		}
		return nil
	}
	body := fmt.Sprintf("Hide %q from the list?\nIts files are kept. U shows hidden sessions.", title)
	switch {
	case it.Tmux != nil && it.State == manager.StateExternal:
		body += fmt.Sprintf("\n\nIt is running: asm first kills tmux session %s and sends SIGTERM to pid %d.", it.Tmux.Name, it.Process.PID)
	case it.Tmux != nil:
		body += fmt.Sprintf("\n\nIt is running: asm first kills tmux session %s, which stops Claude.", it.Tmux.Name)
	case it.Process != nil:
		body += fmt.Sprintf("\n\nIt is running as pid %d outside tmux: asm first sends it SIGTERM.", it.Process.PID)
	}
	m.dialog = &dialog{
		title: "Hide session?",
		body:  body,
		onYes: func(m *Model, _ string) tea.Cmd { return m.stopAndHide(it) },
	}
	return nil
}

// stopAndHide stops the session's tmux session and process, if any, then hides it.
func (m *Model) stopAndHide(it manager.Item) tea.Cmd {
	if it.Tmux == nil && it.Process == nil {
		return m.onHide(hideMsg{item: it, hide: true})
	}
	mgr := m.mgr
	return tea.Batch(m.setStatus("Stopping "+shortID(it.Session.ID)+"…", levelInfo), func() tea.Msg {
		msg := hideMsg{item: it, hide: true, stopped: true}
		if it.Tmux != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if msg.err = mgr.Kill(ctx, it.Session); msg.err != nil {
				return msg
			}
		}
		if it.State == manager.StateExternal {
			msg.err = mgr.Terminate(*it.Process)
		}
		return msg
	})
}

func (m *Model) onHide(msg hideMsg) tea.Cmd {
	var cmds []tea.Cmd
	if msg.stopped {
		cmds = append(cmds, m.scan())
	}
	s := msg.item.Session
	switch {
	case msg.err != nil:
		return tea.Batch(append(cmds, m.setStatus("Not hidden: "+msg.err.Error(), levelError))...)
	case m.isHidden(msg.item) == msg.hide: // already done, e.g. H pressed twice
		return tea.Batch(cmds...)
	}
	if _, err := m.hidden.Toggle(s.Host, s.ID); err != nil {
		return tea.Batch(append(cmds, m.setStatus("hidden: "+err.Error(), levelError))...)
	}
	m.rebuild()
	text := "Unhid " + shortID(s.ID)
	if msg.hide {
		text = "Hid " + shortID(s.ID) + " · U shows hidden sessions"
	}
	return tea.Batch(append(cmds, m.setStatus(text, levelInfo), m.syncPreview(true))...)
}

func (m *Model) openExportDialog(it manager.Item) tea.Cmd {
	s := it.Session
	in := m.newInput(shortPath(filepath.Join(m.cfg.BackupDir, s.ID+".tar.gz")))
	m.dialog = &dialog{
		title: "Export session",
		body:  fmt.Sprintf("Write a backup of %q to:", oneLine(it.Title())),
		input: &in,
		onYes: func(m *Model, path string) tea.Cmd { return m.export(s, path, false) },
	}
	return in.Focus()
}

func (m *Model) export(s provider.Session, path string, replace bool) tea.Cmd {
	mgr := m.mgr
	return tea.Batch(m.setStatus("Exporting "+shortID(s.ID)+"…", levelInfo), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		return exportMsg{session: s, path: path, err: mgr.Export(ctx, s, path, replace)}
	})
}

func (m *Model) onExport(msg exportMsg) tea.Cmd {
	switch {
	case errors.Is(msg.err, fs.ErrExist):
		m.status = ""
		m.dialog = &dialog{
			title: "Replace backup?",
			body:  fmt.Sprintf("%s already exists. Replace it?", msg.path),
			onYes: func(m *Model, _ string) tea.Cmd { return m.export(msg.session, msg.path, true) },
		}
		return nil
	case msg.err != nil:
		return m.setStatus("Export failed: "+msg.err.Error(), levelError)
	}
	return m.setStatus("Exported "+shortID(msg.session.ID)+" to "+msg.path, levelInfo)
}

func (m *Model) openImportDialog() tea.Cmd {
	in := m.newInput(shortPath(m.cfg.BackupDir) + "/")
	m.dialog = &dialog{
		title: "Import session",
		body:  "Backup file to import:",
		input: &in,
		onYes: func(m *Model, path string) tea.Cmd { return m.inspect(path) },
	}
	return in.Focus()
}

func (m *Model) inspect(path string) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		info, err := mgr.Inspect(ctx, path)
		return inspectMsg{path: path, info: info, err: err}
	}
}

// onInspect asks where the backup's project lives on this system, with any
// warnings, before importing it.
func (m *Model) onInspect(msg inspectMsg) tea.Cmd {
	if msg.err != nil {
		return m.setStatus("Import failed: "+msg.err.Error(), levelError)
	}
	b := msg.info
	version := "unknown"
	if b.Version != "" {
		version = "v" + b.Version
	}
	lines := []string{fmt.Sprintf("%q · %s · Claude %s", oneLine(b.Title), shortID(b.SessionID), version)}
	if b.Exists {
		lines = append(lines, m.st.warnText.Render("A session with this ID already exists here. Importing replaces it."))
	}
	if olderVersion(b.LocalVersion, b.Version) {
		lines = append(lines, m.st.warnText.Render(fmt.Sprintf(
			"This system's claude is v%s, older than v%s that wrote the session. It may not load.", b.LocalVersion, b.Version)))
	}
	lines = append(lines, "", "Project directory on this system:")
	in := m.newInput(shortPath(b.CWD))
	m.dialog = &dialog{
		title: "Import session",
		body:  strings.Join(lines, "\n"),
		input: &in,
		onYes: func(m *Model, dir string) tea.Cmd { return m.importBackup(msg.path, b, dir) },
	}
	return in.Focus()
}

func (m *Model) importBackup(path string, b provider.BackupInfo, dir string) tea.Cmd {
	mgr := m.mgr
	return tea.Batch(m.setStatus("Importing "+shortID(b.SessionID)+"…", levelInfo), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		return importMsg{id: b.SessionID, err: mgr.Import(ctx, path, b.SessionID, dir, b.Exists)}
	})
}

func (m *Model) onImport(msg importMsg) tea.Cmd {
	if msg.err != nil {
		return m.setStatus("Import failed: "+msg.err.Error(), levelError)
	}
	m.selID = msg.id // select it once the scan lists it
	return tea.Batch(m.setStatus("Imported "+shortID(msg.id), levelInfo), m.scan())
}

// olderVersion reports whether dotted version local is older than v. Unknown
// or unparsable versions compare as not older.
func olderVersion(local, v string) bool {
	parse := func(s string) []int {
		var out []int
		for _, f := range strings.Split(s, ".") {
			n, err := strconv.Atoi(f)
			if err != nil {
				return nil
			}
			out = append(out, n)
		}
		return out
	}
	a, b := parse(local), parse(v)
	if a == nil || b == nil {
		return false
	}
	return slices.Compare(a, b) < 0
}

// applyNames copies the stored names into the items.
func (m *Model) applyNames() {
	if m.names == nil {
		return
	}
	for i := range m.items {
		s := m.items[i].Session
		m.items[i].Alias = m.names.Get(s.Host, s.ID)
	}
}

// title is the name to show for a session that may not be in the list.
func (m *Model) title(s provider.Session) string {
	if m.names != nil {
		if n := m.names.Get(s.Host, s.ID); n != "" {
			return n
		}
	}
	return s.DisplayTitle()
}

func (m *Model) openRenameDialog(it manager.Item) tea.Cmd {
	if m.names == nil {
		return m.setStatus("Renaming is unavailable: the names file could not be read at startup.", levelError)
	}
	in := m.newInput(oneLine(it.Title()))
	m.dialog = &dialog{
		title: "Rename session",
		body:  "Name shown in asm. Leave empty to show Claude's title again:",
		input: &in,
		onYes: func(m *Model, name string) tea.Cmd { return m.rename(it, name) },
	}
	return in.Focus()
}

func (m *Model) rename(it manager.Item, name string) tea.Cmd {
	name = strings.TrimSpace(name)
	if name == oneLine(it.Title()) {
		return nil // unchanged
	}
	s := it.Session
	if err := m.names.Set(s.Host, s.ID, name); err != nil {
		return m.setStatus("names: "+err.Error(), levelError)
	}
	m.applyNames()
	m.rebuild()
	if name == "" {
		return m.setStatus("Removed the name of "+shortID(s.ID), levelInfo)
	}
	return m.setStatus("Renamed "+shortID(s.ID), levelInfo)
}

func (m *Model) selected() *manager.Item {
	if m.cursor < 0 || m.cursor >= len(m.rows) || m.rows[m.cursor].item < 0 {
		return nil
	}
	return &m.items[m.rows[m.cursor].item]
}

func (m *Model) nextSelectable(i, dir int) int {
	for ; i >= 0 && i < len(m.rows); i += dir {
		if m.rows[i].item >= 0 {
			return i
		}
	}
	return -1
}

func (m *Model) move(delta int) tea.Cmd {
	dir := 1
	if delta < 0 {
		dir, delta = -1, -delta
	}
	cur := m.cursor
	for ; delta > 0; delta-- {
		next := m.nextSelectable(cur+dir, dir)
		if next < 0 {
			break
		}
		cur = next
	}
	return m.setCursor(cur)
}

func (m *Model) setCursor(i int) tea.Cmd {
	if i < 0 || i == m.cursor {
		return nil
	}
	m.cursor = i
	if it := m.selected(); it != nil {
		m.selID = it.Session.ID
	}
	m.ensureVisible()
	// Show cached content now; load after the cursor settles.
	m.previewSeq++
	seq := m.previewSeq
	return tea.Batch(m.syncPreview(false), tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg {
		return previewDueMsg{seq}
	}))
}

func (m *Model) ensureVisible() {
	h := max(m.listH-1, 1) // minus the column header
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	if m.cursor == m.offset && m.cursor > 0 && m.rows[m.cursor-1].item < 0 {
		m.offset-- // keep the group header in view
	}
	m.offset = max(min(m.offset, len(m.rows)-h), 0)
}

func (m *Model) layout() {
	body := m.height - 2 // title and footer
	if m.filterVisible() {
		body--
	}
	m.listH = max(body, 1)
	m.listW = m.width
	if m.previewVisible() {
		m.listW = m.width * 55 / 100
		m.prevW = m.width - m.listW - 2 // border and padding
		m.vp.SetWidth(m.prevW)
		m.vp.SetHeight(max(body-headerLines-1, 1))
	}
	m.filterIn.SetWidth(max(m.width-4, 10))
	m.help.SetWidth(m.width - 1)
	m.ensureVisible()
}

func (m *Model) previewVisible() bool { return m.previewOn && m.width >= 100 }
func (m *Model) filterVisible() bool  { return m.filtering || m.query.raw != "" }

func (m *Model) setQuery(v string) {
	m.query = parseQuery(v, m.now())
	m.rebuild()
}

// --- preview ---

// syncPreview shows the selected session's preview. With load set it starts
// loading content that is not cached; otherwise it only uses the cache.
func (m *Model) syncPreview(load bool) tea.Cmd {
	if !m.previewVisible() {
		return nil
	}
	it := m.selected()
	if it == nil {
		m.previewID = ""
		m.vp.SetContent("")
		return nil
	}
	reset := it.Session.ID != m.previewID
	m.previewID = it.Session.ID

	content, ok, cmd := m.previewContent(*it, load)
	if !ok {
		if !reset {
			return cmd // keep the current content until the new one arrives
		}
		content = m.st.dim.Render("Loading…")
	}
	atBottom := m.vp.AtBottom()
	m.vp.SetContent(content)
	if reset || atBottom {
		m.vp.GotoBottom()
	}
	return cmd
}

func (m *Model) previewContent(it manager.Item, load bool) (string, bool, tea.Cmd) {
	if m.live {
		if it.Tmux == nil {
			return m.st.dim.Render("No tmux session for this conversation.\nPress enter to resume it in tmux, or tab for the transcript."), true, nil
		}
		c, ok := m.captured[it.Session.ID]
		if !ok && load {
			return "", false, m.capture(it)
		}
		return c, ok, nil
	}
	if it.Session.Path == "" {
		return m.st.dim.Render("No transcript yet."), true, nil
	}
	key := m.transcriptKey(it)
	if c, ok := m.transcripts[key]; ok {
		return c, true, nil
	}
	if !load || m.loading[key] {
		return "", false, nil
	}
	m.loading[key] = true
	return "", false, m.loadTranscript(it.Session, key)
}

func (m *Model) loadTranscript(s provider.Session, key transcriptKey) tea.Cmd {
	mgr, n, st, md := m.mgr, m.cfg.PreviewMessages, m.st, m.md
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		t, err := mgr.Load(ctx, s, n)
		if err != nil {
			return transcriptMsg{key, st.errText.Render("Cannot read transcript: " + err.Error())}
		}
		return transcriptMsg{key, renderTranscript(t, key.width, key.dark, st, md)}
	}
}

func (m *Model) capture(it manager.Item) tea.Cmd {
	if m.capturing {
		return nil
	}
	m.capturing = true
	mgr, s := m.mgr, it.Session
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		text, err := mgr.Capture(ctx, s)
		return captureMsg{id: s.ID, text: text, err: err}
	}
}

// --- actions ---

func (m *Model) resume(s provider.Session) tea.Cmd {
	mgr := m.mgr
	return tea.Batch(m.setStatus("Opening "+shortID(s.ID)+"…", levelInfo), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd, err := mgr.Resume(ctx, s)
		return resumeReadyMsg{cmd: cmd, err: err, session: s}
	})
}

func (m *Model) onResumeReady(msg resumeReadyMsg) tea.Cmd {
	var ext *manager.ExternalError
	switch {
	case errors.As(msg.err, &ext):
		p, s := ext.Process, msg.session
		m.dialog = &dialog{
			title: "Session is running outside tmux",
			body: fmt.Sprintf("%q is already running as pid %d, outside its tmux session.\n"+
				"Resuming it now would start a second Claude process writing the same transcript.\n\n"+
				"Send SIGTERM to pid %d and resume the session in tmux?",
				oneLine(m.title(s)), p.PID, p.PID),
			onYes: func(m *Model, _ string) tea.Cmd { return m.terminateAndResume(p, s) },
		}
		m.status = ""
		return nil
	case msg.err != nil:
		return m.setStatus(msg.err.Error(), levelError)
	}
	m.status = ""
	s := msg.session
	return tea.ExecProcess(msg.cmd, func(err error) tea.Msg { return execDoneMsg{err: err, session: s} })
}

func (m *Model) terminateAndResume(p provider.Process, s provider.Session) tea.Cmd {
	mgr := m.mgr
	return tea.Batch(m.setStatus(fmt.Sprintf("Stopping pid %d…", p.PID), levelInfo), func() tea.Msg {
		if err := mgr.Terminate(p); err != nil {
			return resumeReadyMsg{err: err, session: s}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd, err := mgr.Resume(ctx, s)
		return resumeReadyMsg{cmd: cmd, err: err, session: s}
	})
}

// newInput returns a dialog input holding value, with the cursor at its end.
func (m *Model) newInput(value string) textinput.Model {
	in := textinput.New()
	in.Prompt = "> "
	in.SetStyles(textinput.DefaultStyles(m.dark))
	in.SetWidth(min(max(m.width-16, 20), 80))
	in.SetValue(value)
	in.CursorEnd()
	return in
}

func (m *Model) openNewDialog() tea.Cmd {
	dir, _ := os.Getwd()
	if it := m.selected(); it != nil && it.Session.CWD != "" && !it.DirMissing {
		dir = it.Session.CWD
	}
	in := m.newInput(shortPath(dir))
	m.dialog = &dialog{
		title: "New Claude session",
		body:  "Directory to start Claude in:",
		input: &in,
		onYes: func(m *Model, dir string) tea.Cmd { return m.newSession(dir) },
	}
	return in.Focus()
}

func (m *Model) newSession(dir string) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		id, cmd, err := mgr.New(ctx, dir)
		return resumeReadyMsg{cmd: cmd, err: err, session: provider.Session{ID: id, CWD: dir}}
	}
}

func (m *Model) confirmKill(it manager.Item) tea.Cmd {
	if it.Tmux == nil {
		return m.setStatus("This session has no tmux session to kill.", levelError)
	}
	s := it.Session
	m.dialog = &dialog{
		title: "Kill tmux session?",
		body: fmt.Sprintf("Kill tmux session %s?\n"+
			"This stops the Claude process inside it. The transcript is kept and can be resumed.",
			it.Tmux.Name),
		onYes: func(m *Model, _ string) tea.Cmd {
			mgr := m.mgr
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				return actionMsg{text: "Killed tmux session for " + shortID(s.ID), err: mgr.Kill(ctx, s)}
			}
		},
	}
	return nil
}

func (m *Model) setStatus(text string, level statusLevel) tea.Cmd {
	m.status, m.statusLevel = text, level
	m.statusSeq++
	seq := m.statusSeq
	d := 5 * time.Second
	if level != levelInfo {
		d = 12 * time.Second
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return clearStatusMsg{seq} })
}

// --- keys ---

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "ctrl+c" {
		return tea.Quit
	}
	switch {
	case m.dialog != nil:
		return m.handleDialogKey(msg)
	case m.filtering:
		return m.handleFilterKey(msg)
	case m.showHelp:
		m.showHelp = false
		return nil
	}

	k := m.keys
	it := m.selected()
	switch {
	case key.Matches(msg, k.Quit):
		return tea.Quit
	case key.Matches(msg, k.Up):
		return m.move(-1)
	case key.Matches(msg, k.Down):
		return m.move(1)
	case key.Matches(msg, k.PageUp):
		return m.move(-max(m.listH-2, 1))
	case key.Matches(msg, k.PageDown):
		return m.move(max(m.listH-2, 1))
	case key.Matches(msg, k.Top):
		return m.setCursor(m.nextSelectable(0, 1))
	case key.Matches(msg, k.Bottom):
		return m.setCursor(m.nextSelectable(len(m.rows)-1, -1))
	case key.Matches(msg, k.Resume):
		if it != nil {
			return m.resume(it.Session)
		}
	case key.Matches(msg, k.New):
		return m.openNewDialog()
	case key.Matches(msg, k.Kill):
		if it != nil {
			return m.confirmKill(*it)
		}
	case key.Matches(msg, k.Pin):
		if it != nil {
			return m.togglePin(*it)
		}
	case key.Matches(msg, k.Rename):
		if it != nil {
			return m.openRenameDialog(*it)
		}
	case key.Matches(msg, k.Hide):
		if it != nil {
			return m.confirmHide(*it)
		}
	case key.Matches(msg, k.Export):
		if it != nil {
			return m.openExportDialog(*it)
		}
	case key.Matches(msg, k.Import):
		return m.openImportDialog()
	case key.Matches(msg, k.ShowHidden):
		m.showHidden = !m.showHidden
		m.rebuild()
		text := "Hidden sessions are not shown"
		if m.showHidden {
			text = "Showing hidden sessions"
		}
		return tea.Batch(m.setStatus(text, levelInfo), m.syncPreview(true))
	case key.Matches(msg, k.CopyID):
		if it != nil {
			return tea.Batch(tea.SetClipboard(it.Session.ID), m.setStatus("Copied session ID", levelInfo))
		}
	case key.Matches(msg, k.CopyCmd):
		if it != nil {
			return tea.Batch(tea.SetClipboard(m.mgr.ResumeCommand(it.Session)), m.setStatus("Copied resume command", levelInfo))
		}
	case key.Matches(msg, k.Filter):
		m.filtering = true
		m.filterIn.SetValue(m.query.raw)
		m.filterIn.CursorEnd()
		m.layout()
		return m.filterIn.Focus()
	case key.Matches(msg, k.ClearFilter):
		if m.query.raw != "" {
			m.filterIn.SetValue("")
			m.setQuery("")
			m.layout()
			return m.syncPreview(true)
		}
	case key.Matches(msg, k.Sort):
		m.sortBy = (m.sortBy + 1) % numSortKeys
		m.rebuild()
		return m.syncPreview(true)
	case key.Matches(msg, k.Group):
		m.groupBy = (m.groupBy + 1) % numGroupKeys
		m.rebuild()
		return m.syncPreview(true)
	case key.Matches(msg, k.PreviewMode):
		m.live = !m.live
		m.previewID = "" // force a reset to the bottom
		return m.syncPreview(true)
	case key.Matches(msg, k.TogglePreview):
		m.previewOn = !m.previewOn
		m.layout()
		m.previewID = ""
		return m.syncPreview(true)
	case key.Matches(msg, k.ScrollDown):
		m.vp.ScrollDown(1)
	case key.Matches(msg, k.ScrollUp):
		m.vp.ScrollUp(1)
	case key.Matches(msg, k.HalfDown):
		m.vp.HalfPageDown()
	case key.Matches(msg, k.HalfUp):
		m.vp.HalfPageUp()
	case key.Matches(msg, k.Refresh):
		return m.scan()
	case key.Matches(msg, k.Help):
		m.showHelp = true
		mgr := m.mgr
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			return prefixMsg{mgr.TmuxPrefix(ctx)}
		}
	}
	return nil
}

func (m *Model) handleDialogKey(msg tea.KeyPressMsg) tea.Cmd {
	d := m.dialog
	if d.input == nil {
		switch msg.String() {
		case "y", "Y":
			m.dialog = nil
			return d.onYes(m, "")
		case "n", "N", "esc", "q":
			m.dialog = nil
		}
		return nil
	}
	switch msg.String() {
	case "enter":
		m.dialog = nil
		return d.onYes(m, d.input.Value())
	case "esc":
		m.dialog = nil
		return nil
	}
	var cmd tea.Cmd
	*d.input, cmd = d.input.Update(msg)
	return cmd
}

func (m *Model) handleFilterKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		m.filtering = false
		m.filterIn.Blur()
		m.layout()
		return nil
	case "esc":
		m.filtering = false
		m.filterIn.Blur()
		m.filterIn.SetValue("")
		m.setQuery("")
		m.layout()
		return m.syncPreview(true)
	case "up", "down":
		if msg.String() == "up" {
			return m.move(-1)
		}
		return m.move(1)
	}
	var cmd tea.Cmd
	m.filterIn, cmd = m.filterIn.Update(msg)
	if v := m.filterIn.Value(); v != m.query.raw {
		m.setQuery(v)
		return tea.Batch(cmd, m.syncPreview(true))
	}
	return cmd
}

// --- view ---

func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "asm"
	return v
}

func (m *Model) render() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	parts := []string{m.viewTitle()}
	if m.filterVisible() {
		parts = append(parts, m.viewFilter())
	}

	var body string
	if m.showHelp {
		body = m.viewHelp(m.listH)
	} else {
		body = lipgloss.NewStyle().Width(m.listW).Render(m.viewList(m.listW, m.listH))
		if m.previewVisible() {
			body = lipgloss.JoinHorizontal(lipgloss.Top, body, m.viewPreview(m.listH))
		}
	}
	if m.dialog != nil {
		body = lipgloss.Place(m.width, m.listH, lipgloss.Center, lipgloss.Center, m.viewDialog())
	}
	parts = append(parts, body, m.viewFooter())
	return strings.Join(parts, "\n")
}

func (m *Model) viewTitle() string {
	left := m.st.title.Render(" asm") + m.st.titleInfo.Render(" · Claude Code sessions")
	shown, running, hidden := 0, 0, 0
	for _, r := range m.rows {
		if r.item >= 0 {
			shown++
		}
	}
	for _, it := range m.items {
		if it.Process != nil {
			running++
		}
		if m.isHidden(it) {
			hidden++
		}
	}
	info := fmt.Sprintf("%d/%d shown", shown, len(m.items))
	if hidden > 0 {
		info += fmt.Sprintf(" · %d hidden", hidden)
	}
	info += fmt.Sprintf(" · %d running · sort: %s · group: %s ", running, m.sortBy, m.groupBy)
	if !m.loaded {
		info = "scanning… "
	}
	right := m.st.titleInfo.Render(info)
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return ansi.Truncate(left, m.width, "")
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) viewFilter() string {
	line := m.filterIn.View()
	if !m.filtering {
		line = m.st.dim.Render(" / ") + m.query.raw + m.st.dim.Render("   (/ edit · esc clear)")
	}
	if len(m.query.errs) > 0 {
		line += "  " + m.st.errText.Render(strings.Join(m.query.errs, "; "))
	}
	return ansi.Truncate(line, m.width, "…")
}

func (m *Model) viewFooter() string {
	if m.status != "" {
		st := m.st.dim
		switch m.statusLevel {
		case levelWarn:
			st = m.st.warnText
		case levelError:
			st = m.st.errText
		}
		return ansi.Truncate(" "+st.Render(m.status), m.width, "…")
	}
	// Drop the least important hints until the line fits.
	bindings := m.keys.ShortHelp()
	k := m.keys
	for _, drop := range []key.Binding{k.PreviewMode, k.Group, k.Sort, k.New, k.Filter} {
		if line := " " + m.help.ShortHelpView(bindings); lipgloss.Width(line) <= m.width {
			return line
		}
		bindings = slices.DeleteFunc(bindings, func(b key.Binding) bool { return b.Help() == drop.Help() })
	}
	return ansi.Truncate(" "+m.help.ShortHelpView(bindings), m.width, "…")
}

func (m *Model) viewHelp(height int) string {
	legend := []string{}
	for _, s := range []manager.State{manager.StateBusy, manager.StateIdle, manager.StateTmux, manager.StateExternal, manager.StateSaved} {
		desc := map[manager.State]string{
			manager.StateBusy:     "running in its tmux session, working",
			manager.StateIdle:     "running in its tmux session, waiting for input",
			manager.StateTmux:     "tmux session exists; Claude has not reported a process yet",
			manager.StateExternal: "running outside tmux (started elsewhere); cannot attach",
			manager.StateSaved:    "not running; enter resumes it in a new tmux session",
		}[s]
		legend = append(legend, m.st.state[s].Render(fmt.Sprintf("%-10s", stateLabel(s)))+"  "+desc)
	}
	const indent = "  "
	h := m.help
	h.SetWidth(m.width - len(indent))
	content := strings.Join([]string{
		"",
		m.st.title.Render("Keys"),
		h.FullHelpView(m.keys.FullHelp()),
		"",
		m.st.title.Render("Inside a session"),
		m.st.strong.Render(m.tmuxPrefix+" d") + "  " + m.st.dim.Render("return to asm (tmux detach; Claude keeps running)"),
		"",
		m.st.title.Render("States"),
		strings.Join(legend, "\n"),
		"",
		m.st.title.Render("Filter"),
		m.st.dim.Render(filterSyntax),
		"",
		m.st.dim.Render("Press any key to close."),
	}, "\n")

	lines := strings.Split(content, "\n")
	lines = lines[:min(len(lines), height)]
	for i, l := range lines {
		lines[i] = ansi.Truncate(indent+l, m.width, "…")
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m *Model) viewDialog() string {
	d := m.dialog
	parts := []string{m.st.title.Render(d.title), "", d.body}
	if d.input != nil {
		parts = append(parts, d.input.View(), "", m.st.dim.Render("enter confirm · esc cancel"))
	} else {
		parts = append(parts, "", m.st.dim.Render("y yes · n no"))
	}
	return m.st.dialog.Render(strings.Join(parts, "\n"))
}
