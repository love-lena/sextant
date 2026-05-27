package theme

import (
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
)

// NewSpinner returns the one canonical sextant spinner, themed.
//
// Per `conventions/tui-conventions.md` § "Logging, output, and rich
// text" the project ships **one** spinner shape so progress reads
// identically across the chat TUI, the agents TUI, and the dash. The
// Dot frame set is the choice — superfile-style dots, single cell,
// reads well even on slow refresh.
//
// Caller still owns the model: spinner needs to be initialized with
// `s.Tick` and updated on every TickMsg. This helper just bundles the
// frames + foreground binding so no two callers diverge on visuals.
func NewSpinner(th Theme) spinner.Model {
	s := spinner.New(spinner.WithSpinner(spinner.Dot))
	s.Style = lipgloss.NewStyle().Foreground(th.Accent)
	return s
}
