// Package widget is the dash's generic Bubble Tea toolkit: a cursor list, a
// stream viewport, and a detail pane, plus the rounded box chrome they share.
// The widgets are domain-free — they render lists, scrolling streams, and
// wrapped text from a theme.Theme and a Focus state, and know nothing about
// Sextant. By construction they import only lipgloss/bubbletea and the theme
// package — no SDK, no internal/, no nats (an import test enforces this).
//
// Focus is three-state (ADR-0023): a widget is idle, selected (the layout has
// landed on it), or active (the operator stepped in). Each widget renders a
// visible distinction for all three, driven entirely by theme tokens.
package widget

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/pkg/tui/theme"
)

// Focus is a widget's three-state focus, the cue ADR-0023 locks: idle (resting),
// selected (the layout's selection has landed on this pane), and active (the
// operator stepped in and input is routed here).
type Focus int

// The three focus states.
const (
	// FocusIdle is the resting state: a dim border, no selection cue.
	FocusIdle Focus = iota
	// FocusSelected is the layout's current selection: an accent border and a
	// muted cursor cue, but input has not stepped in yet.
	FocusSelected
	// FocusActive is the stepped-in state: an accent border and an active cursor
	// / selection inside the widget.
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
