// Package widget is the dash's generic Bubble Tea toolkit: a cursor list, a
// stream viewport, and a detail pane, plus the rounded box chrome they share.
// The widgets are domain-free — they render lists, scrolling streams, and
// wrapped text from a theme.Theme and a Focus state, and know nothing about
// Sextant. By construction they import only lipgloss/bubbletea and the theme
// package — no SDK, no internal/, no nats (an import test enforces this).
//
// Focus is three-state (ADR-0023, recast by ADR-0026): a widget is idle
// (hidden/resting), selected (visible but unfocused — its place stays readable,
// muted), or active (the focused pane; input is routed here). Each widget
// renders a visible distinction for all three, driven entirely by theme tokens.
package widget

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/theme"
)

// Focus is a widget's three-state focus (ADR-0023's cue, recast by ADR-0026):
// idle (hidden/resting), selected (visible but unfocused), and active (the
// focused pane — input is routed here).
type Focus int

// The three focus states.
const (
	// FocusIdle is the resting state: a dim border, no selection cue.
	FocusIdle Focus = iota
	// FocusSelected is the visible-but-unfocused state: an accent border and a
	// muted cursor cue, so the pane's place stays readable while the operator
	// works elsewhere.
	FocusSelected
	// FocusActive is the focused state: an accent border and an active cursor
	// / selection inside the widget; keys are delivered here.
	FocusActive
)

// borderColor returns the box border colour for a focus state: the resting dim
// line when idle, the accent when selected or active. The inside-the-widget cue
// (a visible cursor) is what further separates selected from active.
func (f Focus) borderColor(t theme.Theme) lipgloss.Color {
	if f == FocusIdle {
		return t.Line
	}
	return t.Accent
}

// keyMatches reports whether a key message matches a binding. It is the one
// place widgets consult a binding — keys are data (theme.Keymap), so a widget
// never compares against a literal key string. A burst/pasted chunk never
// matches: pasted text is content, so a chunk spelling "up" must not scroll a
// list or stream.
func keyMatches(msg tea.KeyMsg, b key.Binding) bool {
	return !IsTextChunk(msg) && key.Matches(msg, b)
}

// IsTextChunk reports whether a key message is burst/pasted text rather than a
// single keystroke: a bracketed paste (KeyMsg.Paste — even a single character,
// which is otherwise indistinguishable from the keystroke) or a multi-rune
// KeyRunes (an unbracketed paste or a fast input burst arrives as one chunk).
// A chunk's String() can spell a binding name ("esc", "enter", "q") and
// binding matches compare strings, so a chunk must never be matched against
// bindings — it is content, period. The upper strata (surface, layout) apply
// the same discipline to their own binding matches; widget is the lowest
// stratum, so the one predicate lives here.
func IsTextChunk(msg tea.KeyMsg) bool {
	return msg.Paste || (msg.Type == tea.KeyRunes && len(msg.Runes) > 1)
}
