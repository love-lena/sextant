package surface_test

import (
	tea "github.com/charmbracelet/bubbletea"
)

// keyRune builds a single-rune key press, the message a terminal sends for a
// typed character. The compose tests feed these to drive the textinput.
func keyRune(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}
