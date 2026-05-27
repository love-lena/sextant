package component

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// Component is the contract every Tier 1 TUI implements on top of
// `tea.Model`. Method semantics:
//
//   - SetSize informs the component of the content rect it owns. The
//     host computes the rect (total terminal size minus its own
//     chrome) and pushes it down on every tea.WindowSizeMsg.
//
//   - Focus / Blur / Focused track whether the component is the
//     currently-interactive surface. Standalone hosts call Focus once
//     at startup and never Blur; the dash drives these as focus moves
//     between panes. Focus may return a tea.Cmd (e.g. a cursor blink
//     subscription); Blur must not.
//
//   - ShortHelp / FullHelp expose the component's key bindings to the
//     host's help-bar renderer. The convention follows bubbles/help:
//     short help is one row of the most useful bindings; full help is
//     a grid of columns, one column per topical group.
//
// A component must be runnable standalone *and* mountable in the dash
// with no code changes. Same model, different host.
type Component interface {
	tea.Model // Init, Update, View

	SetSize(w, h int)

	Focus() tea.Cmd
	Blur()
	Focused() bool

	ShortHelp() []key.Binding
	FullHelp() [][]key.Binding
}
