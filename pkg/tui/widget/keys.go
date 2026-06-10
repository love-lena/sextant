package widget

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// keyMatches reports whether a key message matches a binding. It is the one
// place widgets consult a binding — keys are data (theme.Keymap), so a widget
// never compares against a literal key string. A burst/pasted chunk never
// matches: pasted text is content, so a chunk spelling "up" must not scroll a
// list or stream.
func keyMatches(msg tea.KeyMsg, b key.Binding) bool {
	return !IsTextChunk(msg) && key.Matches(msg, b)
}

// IsTextChunk reports whether a key message is burst/pasted text rather than a
// single keystroke: a bracketed paste (KeyMsg.Paste — even a single character,
// which is otherwise indistinguishable from the keystroke) or a multi-rune
// KeyRunes (an unbracketed paste or a fast input burst arrives as one chunk).
// A chunk's String() can spell a binding name ("esc", "enter", "q") and
// binding matches compare strings, so a chunk must never be matched against
// bindings — it is content, period. The upper strata (surface, layout) apply
// the same discipline to their own binding matches; widget is the lowest
// stratum, so the one predicate lives here.
func IsTextChunk(msg tea.KeyMsg) bool {
	return msg.Paste || (msg.Type == tea.KeyRunes && len(msg.Runes) > 1)
}
