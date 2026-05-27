package theme

import "github.com/charmbracelet/lipgloss"

// DefaultTheme returns the built-in adaptive role table — the theme
// sextant ships when no `themes/*.yaml` is loaded.
//
// Every color is `lipgloss.AdaptiveColor{Light, Dark}`. Bare ANSI
// numbers are theme-dependent (`"237"` is near-black on light
// terminals; `"8"` is invisible on light) and bit us in the chat-TUI
// rollout — see `conventions/tui-conventions.md`
// § "Adaptive colors are the default".
//
// Values lean on the standard 8-color ANSI block where the contrast
// gap between light and dark is wide enough to matter; otherwise they
// pick 256-color slots that read well on both backgrounds.
func DefaultTheme() Theme {
	return Theme{
		Name: "default",

		// Structural.
		Background:      lipgloss.AdaptiveColor{Light: "255", Dark: "0"},
		BackgroundAlt:   lipgloss.AdaptiveColor{Light: "254", Dark: "237"},
		Foreground:      lipgloss.AdaptiveColor{Light: "0", Dark: "15"},
		ForegroundMuted: lipgloss.AdaptiveColor{Light: "243", Dark: "8"},
		Border:          lipgloss.AdaptiveColor{Light: "247", Dark: "244"},
		BorderActive:    lipgloss.AdaptiveColor{Light: "27", Dark: "12"},

		// Signal.
		Accent:  lipgloss.AdaptiveColor{Light: "27", Dark: "4"},
		Danger:  lipgloss.AdaptiveColor{Light: "1", Dark: "9"},
		Warning: lipgloss.AdaptiveColor{Light: "3", Dark: "3"},
		Success: lipgloss.AdaptiveColor{Light: "2", Dark: "2"},
	}
}
