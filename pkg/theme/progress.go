package theme

import (
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
)

// NewProgress returns the one canonical sextant progress bar, themed.
//
// Per `conventions/tui-conventions.md` § "Logging, output, and rich
// text" the project ships **one** progress style. Solid fill in the
// theme's `accent` color — no rainbow gradients, no per-component
// recoloring. Operators learn one visual once.
//
// The progress bar reads a hex string for `WithSolidFill`. We resolve
// the theme's Accent into the closest hex we can:
//
//   - `lipgloss.Color` is a string type that can already be a hex
//     (`#RRGGBB`) or an ANSI index (`"4"`). Pass it through.
//   - `lipgloss.AdaptiveColor` resolves at render time depending on
//     terminal background; we pick the Dark value because the design
//     target is dark terminals.
//   - Anything else: fall back to a known-good blue. The progress bar
//     still renders; only the accent shade shifts.
func NewProgress(th Theme) progress.Model {
	fill := accentHex(th.Accent)
	return progress.New(progress.WithSolidFill(fill), progress.WithoutPercentage())
}

// accentHex resolves a TerminalColor to a value the progress bar's
// solid-fill option can accept.
func accentHex(c lipgloss.TerminalColor) string {
	switch v := c.(type) {
	case lipgloss.Color:
		return string(v)
	case lipgloss.AdaptiveColor:
		if v.Dark != "" {
			return v.Dark
		}
		return v.Light
	default:
		return "#1E66F5"
	}
}
