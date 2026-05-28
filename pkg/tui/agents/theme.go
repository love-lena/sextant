// theme.go — local Lipgloss styles for the agents Component.
//
// Role tokens live in `pkg/theme/`. This file binds them into the
// local `theme` struct the model layer consults. Adding a new style
// here means picking a role from `pkg/theme.Theme`, not introducing
// a bare palette index.
package agents

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/love-lena/sextant/pkg/theme"
)

// localTheme holds the local style table. Fields are populated from
// `pkg/theme.Theme` via themeFor.
type localTheme struct {
	title     lipgloss.Style
	header    lipgloss.Style
	row       lipgloss.Style
	rowActive lipgloss.Style
	muted     lipgloss.Style
	status    lipgloss.Style
	help      lipgloss.Style
	errorBar  lipgloss.Style
}

// defaultTheme returns the baseline styles hydrated from `pkg/theme`'s
// built-in adaptive theme. Tests and standalone runs land here; an
// operator who sets `theme = "..."` in `~/.config/sextant/config.toml`
// gets their loaded scheme via themeFor.
func defaultTheme() localTheme { return themeFor(theme.DefaultTheme()) }

// themeFor binds a `pkg/theme.Theme` into the local style table.
func themeFor(th theme.Theme) localTheme {
	return localTheme{
		title:     lipgloss.NewStyle().Bold(true).Foreground(th.Accent),
		header:    lipgloss.NewStyle().Bold(true).Foreground(th.ForegroundMuted),
		row:       lipgloss.NewStyle(),
		rowActive: lipgloss.NewStyle().Reverse(true),
		muted:     lipgloss.NewStyle().Foreground(th.ForegroundMuted),
		status:    lipgloss.NewStyle().Foreground(th.ForegroundMuted),
		help:      lipgloss.NewStyle().Foreground(th.Foreground),
		errorBar:  lipgloss.NewStyle().Bold(true).Foreground(th.Danger),
	}
}
