package component

import (
	tea "github.com/charmbracelet/bubbletea"
)

// ChromeFunc draws chrome (titles, borders, status bars) around a
// component's content. width/height are the full terminal dimensions
// the host received; content is the component's rendered output
// (already laid out into its content rect). Pure function: no
// hidden state. Returns the final frame the host sends to bubbletea.
//
// A standalone wrapper at `pkg/tui/<surface>/standalone.go` provides
// a ChromeFunc that draws the surface's specific header / status
// bar. The dash provides a different ChromeFunc when mounting the
// same component as a pane.
type ChromeFunc func(width, height int, content string) string

// Host wraps a Component for standalone use. Responsibilities:
//
//   - Translates tea.WindowSizeMsg into Component.SetSize(content
//     rect). The content rect is `width × (height - chromeReserved)`,
//     where chromeReserved is the number of rows the chrome takes.
//   - Routes intent messages (DoneMsg → tea.Quit). OpenMsg /
//     LoadMsg are passed through to the component; standalone hosts
//     don't have other panes to route to.
//   - Forwards all other messages to the component verbatim.
//   - Composes Chrome(width, height, content) around the component's
//     View() output. If Chrome is nil, the component's View is
//     returned bare.
//
// Construct via NewHost. Zero value is invalid (no inner component).
type Host struct {
	inner            Component
	chrome           ChromeFunc
	chromeReserved   int // rows the chrome occupies (subtracted from height before SetSize)
	width, height    int
	initialLoadID    string // sent as a LoadMsg on Init, if non-empty
	wantInitialFocus bool
}

// HostOption tunes a Host at construction time. Options are functional
// rather than struct-fields so future additions don't break callers.
type HostOption func(*Host)

// WithChrome installs a chrome renderer. height passed to the chrome
// func is the total terminal height; the component already sees only
// its content rect (height minus reserved).
func WithChrome(fn ChromeFunc, reserved int) HostOption {
	return func(h *Host) {
		h.chrome = fn
		h.chromeReserved = reserved
	}
}

// WithInitialLoad fires a LoadMsg{ID: id} on Init. The convention is
// "the standalone wrapper fires one LoadMsg in Init" so the component
// uses the same code path it would use when re-targeted by the dash.
func WithInitialLoad(id string) HostOption {
	return func(h *Host) {
		h.initialLoadID = id
	}
}

// WithInitialFocus calls Focus() on the wrapped component during Init
// and chains the returned cmd. Standalone hosts almost always want
// this; the dash decides focus per pane.
func WithInitialFocus() HostOption {
	return func(h *Host) {
		h.wantInitialFocus = true
	}
}

// NewHost wraps c with standalone-host behavior. Apply HostOptions to
// install a chrome renderer, fire an initial LoadMsg, or claim focus
// on startup.
func NewHost(c Component, opts ...HostOption) *Host {
	h := &Host{inner: c}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Init runs the component's Init, optionally focuses it, and optionally
// fires an initial LoadMsg. Returned cmds are batched.
func (h *Host) Init() tea.Cmd {
	cmds := []tea.Cmd{h.inner.Init()}
	if h.wantInitialFocus {
		if cmd := h.inner.Focus(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if h.initialLoadID != "" {
		id := h.initialLoadID
		cmds = append(cmds, func() tea.Msg { return LoadMsg{ID: id} })
	}
	return tea.Batch(cmds...)
}

// Update routes messages. WindowSizeMsg → SetSize on the component
// (with chrome reserved subtracted from height). DoneMsg → tea.Quit.
// All other messages pass through to the component.
//
// Components in this codebase use value-receiver tea.Model semantics
// (Update returns the new state), so we must capture the return and
// re-store it in h.inner. The type assertion will panic only if a
// component returns a tea.Model that isn't also a Component, which
// would be a programming error worth crashing on.
func (h *Host) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		h.width, h.height = m.Width, m.Height
		innerH := m.Height - h.chromeReserved
		if innerH < 1 {
			innerH = 1
		}
		h.inner.SetSize(m.Width, innerH)
		// Forward the original message so components that key other
		// behavior off WindowSizeMsg (e.g. textareas inside the
		// component) still see it.
		next, cmd := h.inner.Update(msg)
		h.inner = next.(Component)
		return h, cmd
	case DoneMsg:
		return h, tea.Quit
	default:
		_ = m
	}
	next, cmd := h.inner.Update(msg)
	h.inner = next.(Component)
	return h, cmd
}

// View composes Chrome(width, height, Component.View()). If no
// chrome renderer was installed, the component's View is returned
// bare.
func (h *Host) View() string {
	content := h.inner.View()
	if h.chrome == nil {
		return content
	}
	return h.chrome(h.width, h.height, content)
}

// Inner returns the wrapped component. Exposed for tests and for
// callers that need to drive the model directly (e.g. preview
// binaries that want to seed state).
func (h *Host) Inner() Component { return h.inner }
