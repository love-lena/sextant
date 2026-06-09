package layout

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// statusBar renders the one-row hint bar along the bottom: the current
// selection + level on the left, the context-appropriate key hints on the right,
// painted on the panel background and clamped to the terminal width.
func (m Model) statusBar() string {
	left := m.selected
	if left == "" {
		left = "—"
	}
	state := "selected"
	if m.level == levelPane {
		state = "active"
	}
	leftSeg := lipgloss.NewStyle().Foreground(m.th.Fg).Render(
		fmt.Sprintf(" %s · %s · %s ", m.preset, left, state),
	)

	var hints string
	switch {
	case m.menu != nil:
		hints = "↑/↓ move · enter select · esc close "
	case m.level == levelPane:
		hints = "esc back · ↑/↓ within · ^c quit "
	default:
		hints = "↑↓←→ move · enter step in · p preset · o options · q quit "
	}
	hint := lipgloss.NewStyle().Foreground(m.th.Dim).Render(hints)

	gap := m.w - lipgloss.Width(leftSeg) - lipgloss.Width(hint)
	if gap < 1 {
		gap = 1
	}
	bar := leftSeg + strings.Repeat(" ", gap) + hint
	return lipgloss.NewStyle().Background(m.th.Panel).Width(m.w).MaxWidth(m.w).MaxHeight(1).Render(bar)
}
