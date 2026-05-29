// model.go — contextview Component: a tailing viewport over an agent's
// SDK session JSONL with switchable view modes (sessionlog.Mode).
package contextview

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/love-lena/sextant/pkg/sessionlog"
	"github.com/love-lena/sextant/pkg/tui/component"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// maxBufferLines caps the viewport ring so an overnight tail can't grow
// unbounded.
const maxBufferLines = 20000

// Options configure a Model. Events is the live session stream (the
// launcher wires sessionlog.Stream over a tailed file); tests leave it
// nil and drive eventMsg directly. InitialMode defaults to raw.
type Options struct {
	Events      <-chan sessionlog.Event
	InitialMode sessionlog.Mode
}

// Model implements component.Component.
type Model struct {
	ch      <-chan sessionlog.Event
	events  []sessionlog.Event
	mode    sessionlog.Mode
	sv      *widget.StreamViewport
	lines   []string // last rendered buffer (for tests)
	keys    keymap
	focused bool
	w, h    int
}

type keymap struct {
	Quit   key.Binding
	Modes  key.Binding
	Scroll key.Binding
}

func defaultKeys() keymap {
	return keymap{
		Quit:   key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Modes:  key.NewBinding(key.WithKeys("1", "2", "3", "4", "5", "6"), key.WithHelp("1-6", "mode")),
		Scroll: key.NewBinding(key.WithKeys("j", "k", "pgup", "pgdown"), key.WithHelp("j/k", "scroll")),
	}
}

// New constructs a contextview Model.
func New(opts Options) *Model {
	mode := opts.InitialMode
	if mode == "" {
		mode = sessionlog.ModeRaw
	}
	return &Model{
		ch:   opts.Events,
		mode: mode,
		sv:   widget.NewStreamViewport(maxBufferLines),
		keys: defaultKeys(),
	}
}

// --- messages ---

type eventMsg struct {
	ev sessionlog.Event
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
	return []key.Binding{m.keys.Scroll, m.keys.Modes, m.keys.Quit}
}

func (m *Model) FullHelp() [][]key.Binding {
	return [][]key.Binding{{m.keys.Scroll}, {m.keys.Modes, m.keys.Quit}}
}

// Init starts pumping the event stream (if one was supplied).
func (m *Model) Init() tea.Cmd { return m.readNextCmd() }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, nil
	case component.LoadMsg:
		return m, nil // stream is wired at construction; LoadMsg is a no-op
	case eventMsg:
		if !msg.ok {
			return m, nil // stream closed
		}
		m.events = append(m.events, msg.ev)
		m.appendRendered(msg.ev)
		return m, m.readNextCmd()
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func emitDone() tea.Msg { return component.DoneMsg{} }

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Quit) {
		return m, emitDone
	}
	if s := msg.String(); len(s) == 1 && s[0] >= '1' && s[0] <= '6' {
		idx := int(s[0] - '1')
		if idx < len(sessionlog.AllModes) {
			m.mode = sessionlog.AllModes[idx]
			m.rebuild()
		}
		return m, nil
	}
	// Everything else is scroll — forward to the viewport.
	cmd := m.sv.Update(msg)
	return m, cmd
}

// appendRendered renders one freshly-arrived event in the current mode
// and appends it (tail). Usage mode needs running totals, so it can't be
// incremental — fall back to a full rebuild there.
func (m *Model) appendRendered(ev sessionlog.Event) {
	if m.mode == sessionlog.ModeUsage {
		m.rebuild()
		return
	}
	if s := sessionlog.RenderLine(ev, m.mode, nil); s != "" {
		parts := strings.Split(s, "\n")
		m.lines = append(m.lines, parts...)
		m.sv.Append(parts...)
	}
}

// rebuild re-renders the whole buffer in the current mode (used on mode
// switch and for usage mode's running totals).
func (m *Model) rebuild() {
	var acc *sessionlog.UsageAccumulator
	if m.mode == sessionlog.ModeUsage {
		acc = &sessionlog.UsageAccumulator{}
	}
	lines := make([]string, 0, len(m.events))
	for _, ev := range m.events {
		if s := sessionlog.RenderLine(ev, m.mode, acc); s != "" {
			lines = append(lines, strings.Split(s, "\n")...)
		}
	}
	m.lines = lines
	m.sv.SetContent(lines)
}

func (m *Model) View() string { return m.sv.View() }

// Mode returns the active view mode (for tests / the chrome).
func (m *Model) Mode() sessionlog.Mode { return m.mode }

// renderedBuffer returns the current rendered content (for tests).
func (m *Model) renderedBuffer() string { return strings.Join(m.lines, "\n") }

func (m *Model) readNextCmd() tea.Cmd {
	ch := m.ch
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		return eventMsg{ev: ev, ok: ok}
	}
}
