package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/ehsanaghajouei/agents-session-manager/internal/idset"
	"github.com/ehsanaghajouei/agents-session-manager/internal/names"
)

// Preferences holds one provider's local session preferences. A nil store
// disables the corresponding action.
type Preferences struct {
	Pins   *idset.Set
	Names  *names.Store
	Hidden *idset.Set
}

func (m *Model) prefs() Preferences { return m.preferences[m.provider] }

type providerView int

const (
	claudeView providerView = iota
	openCodeView
	numProviderViews
)

func (v providerView) String() string {
	if v == openCodeView {
		return "OpenCode"
	}
	return "Claude Code"
}

func (m *Model) switchProvider() tea.Cmd {
	if m.openCodeScan == nil {
		return nil
	}
	// An action may still need to show a confirmation or attach to tmux.
	if m.pendingActions > 0 {
		return m.setStatus("Wait for the "+m.provider.String()+" action to finish before switching providers.", levelInfo)
	}
	if cancel := m.scanCancel[m.provider]; cancel != nil {
		cancel()
	}
	m.providerSelection[m.provider] = m.selID
	clear(m.openCodeMarks)
	m.provider = (m.provider + 1) % numProviderViews
	m.providerSeq++
	m.selID = m.providerSelection[m.provider]
	m.items, m.rows = nil, nil
	m.cursor, m.offset = 0, 0
	m.loaded, m.scanFailed, m.rescan = false, false, false
	m.warnings, m.status = "", ""
	m.statusSeq++
	m.previewID = ""
	m.previewSeq++
	m.cancelTranscript()
	m.vp.SetContent("")
	m.capturing = false
	clear(m.transcripts)
	clear(m.captured)
	return m.startView()
}

func (m *Model) startView() tea.Cmd {
	if warnings := m.startup[m.provider]; len(warnings) > 0 {
		m.startup[m.provider] = nil
		status := m.setStatus(m.provider.String()+": "+strings.Join(warnings, "; "), levelWarn)
		return tea.Batch(status, m.scan())
	}
	return m.scan()
}

func (m *Model) helpKeys() keyMap {
	k := m.keys
	k.SwitchProvider.SetEnabled(m.openCodeScan != nil)
	k.Delete.SetEnabled(m.provider == openCodeView && m.openCodeDelete != nil)
	k.Mark.SetEnabled(k.Delete.Enabled())
	if m.provider == openCodeView {
		k.Resume.SetEnabled(m.openCodeResume != nil)
		k.New.SetEnabled(m.openCodeNew != nil)
		for _, binding := range k.claudeOnly() {
			binding.SetEnabled(false)
		}
	}
	return k
}

// pendingAction keeps the provider view active until an asynchronous action's
// result can be applied.
func (m *Model) pendingAction(run tea.Cmd) tea.Cmd {
	m.pendingActions++
	return func() tea.Msg { return pendingActionMsg{run()} }
}
