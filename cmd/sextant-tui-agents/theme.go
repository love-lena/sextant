// theme.go — local Lipgloss styles for sextant-tui-agents.
//
// M13 ships one TUI; the conventions doc names tokens (Success / Warning /
// Error / Info / TextPrimary / TextMuted / Border / Highlight) that should
// eventually live in `pkg/theme/`. Until a second TUI needs them we keep
// the styles file-local — promoting them now would build an abstraction
// against a single caller. Moving to `pkg/theme/` is tracked as the
// follow-up alongside the next TUI (see plans/bootstrap.md after M13).
//
// Plan: plans/bootstrap.md#M13
package main

import "github.com/charmbracelet/lipgloss"

// theme holds the four logical roles M13 needs. Other roles
// (Warning/Info) land when a TUI needs them.
type theme struct {
	title     lipgloss.Style
	header    lipgloss.Style
	row       lipgloss.Style
	rowActive lipgloss.Style
	muted     lipgloss.Style
	status    lipgloss.Style
	help      lipgloss.Style
	errorBar  lipgloss.Style
}

// defaultTheme returns the M13 baseline styles. Colors are ANSI numbers
// so the TUI renders identically across terminal palettes — Lipgloss
// degrades named colors on non-256 terminals, which matters because the
// operator may SSH in.
func defaultTheme() theme {
	return theme{
		title:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")), // bright blue
		header:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("8")),  // bright black / dim
		row:       lipgloss.NewStyle(),
		rowActive: lipgloss.NewStyle().Reverse(true),
		muted:     lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		status:    lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		help:      lipgloss.NewStyle().Foreground(lipgloss.Color("7")),
		errorBar:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9")), // bright red
	}
}
