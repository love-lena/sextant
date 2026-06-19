package layout

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// statusBar renders the one-row hint bar along the bottom: the focused pane
// (its id and live title — ADR-0026: the bar names the focused pane, there is
// no layout/pane mode to report) on the left, the key hints on the right,
// painted on the panel background and clamped to the terminal width.
func (m Model) statusBar() string {
	left := "—"
	if m.focused != "" {
		left = m.focused
		if title := m.surfaces[m.focused].Title(); title != "" {
			left = fmt.Sprintf("%s — %s", m.focused, title)
		}
	}
	leftSeg := lipgloss.NewStyle().Foreground(m.th.Fg).Render(" " + left + " ")

	hint := lipgloss.NewStyle().Foreground(m.th.Dim).Render(m.hints())

	gap := m.w - lipgloss.Width(leftSeg) - lipgloss.Width(hint)
	if gap < 1 {
		gap = 1
	}
	bar := leftSeg + strings.Repeat(" ", gap) + hint
	return lipgloss.NewStyle().Background(m.th.Panel).Width(m.w).MaxWidth(m.w).MaxHeight(1).Render(bar)
}

// hints returns the right-hand key hints for the current state. While the
// focused surface is capturing text, q types rather than quits, so the quit
// hint switches to the always-true ctrl+c (the hints stay honest).
func (m Model) hints() string {
	if m.menu != nil {
		return "↑/↓ move · enter select · esc close "
	}
	quit := "q quit"
	if m.focusedCapturing() {
		quit = "^c quit"
	}
	return "tab/^hjkl focus · enter open · esc back · " + quit + " "
}
