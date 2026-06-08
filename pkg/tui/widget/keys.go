package widget

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// keyMatches reports whether a key message matches a binding. It is the one
// place widgets consult a binding — keys are data (theme.Keymap), so a widget
// never compares against a literal key string.
func keyMatches(msg tea.KeyMsg, b key.Binding) bool {
	return key.Matches(msg, b)
}
