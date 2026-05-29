// model.go — logsview Component: a tailing viewport over the daemon log
// file, fed by a widget.Source[string] (the launcher wires a TailSource).
package logsview

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/love-lena/sextant/pkg/tui/component"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

const maxBufferLines = 20000

// Options configure a Model. Events is the line stream (the launcher
// wires a widget.TailSource over the log file); tests leave it nil and
// drive lineMsg directly.
type Options struct {
	Events <-chan widget.Event[string]
}

// Model implements component.Component.
type Model struct {
	ch      <-chan widget.Event[string]
	sv      *widget.StreamViewport
	errMsg  string
	keys    keymap
	focused bool
	w, h    int
}

type keymap struct {
	Quit   key.Binding
	Scroll key.Binding
}

func defaultKeys() keymap {
	return keymap{
		Quit:   key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Scroll: key.NewBinding(key.WithKeys("j", "k", "pgup", "pgdown", "g", "G"), key.WithHelp("j/k", "scroll")),
	}
}

// New constructs a logsview Model.
func New(opts Options) *Model {
	return &Model{
		ch:   opts.Events,
		sv:   widget.NewStreamViewport(maxBufferLines),
		keys: defaultKeys(),
	}
}

// --- messages ---

type lineMsg struct {
	ev widget.Event[string]
	ok bool
}

// --- Component interface ---

func (m *Model) SetSize(w, h int) {
	m.w, m.h = w, h
	m.sv.SetSize(w, h)
}

func (m *Model) Focus() tea.Cmd { m.focused = true; return nil }
func (m *Model) Blur()          { m.focused = false }
func (m *Model) Focused() bool  { return m.focused }

func (m *Model) ShortHelp() []key.Binding {
	return []key.Binding{m.keys.Scroll, m.keys.Quit}
}

func (m *Model) FullHelp() [][]key.Binding {
	return [][]key.Binding{{m.keys.Scroll, m.keys.Quit}}
}

// Init starts pumping the log stream.
func (m *Model) Init() tea.Cmd { return m.readNextCmd() }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, nil
	case component.LoadMsg:
		return m, nil
	case lineMsg:
		if !msg.ok {
			return m, nil // stream closed
		}
		if msg.ev.Err != nil {
			m.errMsg = msg.ev.Err.Error()
			return m, m.readNextCmd()
		}
		m.sv.Append(msg.ev.Item)
		return m, m.readNextCmd()
	case tea.KeyMsg:
		if key.Matches(msg, m.keys.Quit) {
			return m, func() tea.Msg { return component.DoneMsg{} }
		}
		cmd := m.sv.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) View() string {
	if m.errMsg != "" {
		return "! " + m.errMsg + "\n" + m.sv.View()
	}
	return m.sv.View()
}

// Following reports whether the viewport is tailing (for the chrome).
func (m *Model) Following() bool { return m.sv.Following() }

// LineCount returns the buffered line count (for tests).
func (m *Model) LineCount() int { return m.sv.LineCount() }

func (m *Model) readNextCmd() tea.Cmd {
	ch := m.ch
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		return lineMsg{ev: ev, ok: ok}
	}
}
