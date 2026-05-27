package theme

import "github.com/charmbracelet/lipgloss"

// Theme is the role-token bundle every TUI / CLI surface consumes.
// Fields divide into two groups, per `conventions/tui-conventions.md`
// § "Visual design language → Color":
//
//   - Structural carries chrome (panes, dividers, text body).
//   - Signal carries meaning (focus, errors, warnings, completion).
//
// Both are TerminalColor so callers may bind adaptive (no-theme
// default) or fixed (loaded base16 file) palettes through the same
// surface. Use the Style helper methods or read fields directly into
// `lipgloss.NewStyle().Foreground(...)`.
//
// Zero value is invalid; obtain a Theme via DefaultTheme() or
// LoadBase16().
type Theme struct {
	// --- Structural ---

	// Background is the canvas color for panes and surfaces.
	Background lipgloss.TerminalColor
	// BackgroundAlt is the alternate canvas (selection tint, secondary panes).
	BackgroundAlt lipgloss.TerminalColor
	// Foreground is the default text color.
	Foreground lipgloss.TerminalColor
	// ForegroundMuted is de-emphasized text (timestamps, hints, dividers).
	ForegroundMuted lipgloss.TerminalColor
	// Border is the inactive pane border color.
	Border lipgloss.TerminalColor
	// BorderActive is the focused pane border color.
	BorderActive lipgloss.TerminalColor

	// --- Signal ---

	// Accent is the one signal color per screen (selection, focus).
	Accent lipgloss.TerminalColor
	// Danger surfaces errors, failed tool calls, destructive actions.
	Danger lipgloss.TerminalColor
	// Warning surfaces operator-attention states.
	Warning lipgloss.TerminalColor
	// Success surfaces completion / OK tool calls.
	Success lipgloss.TerminalColor

	// Name records which theme this is (e.g. "default", "tomorrow-night").
	// Empty for the built-in adaptive default. Diagnostic only.
	Name string
}

// Empty reports whether the theme has no roles populated. Useful as a
// guard for "did the loader return a usable theme?".
func (t Theme) Empty() bool {
	return t.Background == nil &&
		t.Foreground == nil &&
		t.Accent == nil &&
		t.Border == nil
}
