package chat

import "github.com/charmbracelet/bubbles/key"

// keyMap is the key vocabulary for the chat TUI. Separated by mode:
// the reducer matches against the active mode's bindings, and the
// status bar renders the same bindings as hint chips. Spec §"Mode-
// aware status bar": only the keys that work in the current mode are
// shown — no busy legend of inert hotkeys.
type keyMap struct {
	// NORMAL mode
	NormalUp      key.Binding
	NormalDown    key.Binding
	NormalTop     key.Binding // 'gg' (two-key)
	NormalBottom  key.Binding // 'G'
	NormalInsert  key.Binding // 'i'
	NormalQuit    key.Binding // 'q' / ctrl+c
	NormalRestart key.Binding // 'R' — restart the agent (only active in lost state)

	// INSERT mode
	InsertSend    key.Binding // enter
	InsertNewline key.Binding // shift+enter
	InsertExit    key.Binding // esc
}

func defaultKeys() keyMap {
	return keyMap{
		NormalUp:      key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k", "up")),
		NormalDown:    key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j", "down")),
		NormalTop:     key.NewBinding(key.WithKeys("g"), key.WithHelp("gg", "top")),
		NormalBottom:  key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "bottom")),
		NormalInsert:  key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "edit")),
		NormalQuit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		NormalRestart: key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "restart")),
		InsertSend:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "send")),
		InsertNewline: key.NewBinding(key.WithKeys("shift+enter"), key.WithHelp("⇧↵", "newline")),
		InsertExit:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", "back")),
	}
}
