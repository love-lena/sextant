package theme

import "github.com/charmbracelet/bubbles/key"

// Keymap is the dash's binding set as data: one named key.Binding per action.
// Nothing in a widget hardcodes a key — widgets read bindings from a Keymap, so
// the bindings are overridable defaults, not a contract. The default reflects
// the two-level focus model (ADR-0023): layout-level navigation moves the
// selected pane and Enter steps in; pane-level navigation moves within and Esc
// steps out.
//
// Keys are expected to churn. Treat DefaultKeymap as a starting point and use
// Merge to layer a user's overrides on top.
type Keymap struct {
	// Up and Down move the cursor / scroll within a pane, or move the selected
	// pane vertically at the layout level.
	Up   key.Binding
	Down key.Binding
	// Left and Right move horizontally — between panes at the layout level, or
	// across columns within a pane.
	Left  key.Binding
	Right key.Binding

	// Enter steps into the selected pane (layout level): the pane goes from
	// selected to active. In a conversation surface, Enter sends the composed
	// message.
	Enter key.Binding
	// Back steps out one level (active pane → selected). It never quits; Esc is
	// strictly "step out".
	Back key.Binding

	// Options opens the universal options menu.
	Options key.Binding
	// Quit leaves the dash cleanly.
	Quit key.Binding
	// ForceQuit is the hard quit (Ctrl-C), bypassing any confirmation.
	ForceQuit key.Binding
}

// DefaultKeymap returns the locked default binding set (ADR-0023). Arrows and
// hjkl both navigate; Enter steps in; Esc steps out; o opens options; q quits
// and Ctrl-C hard-quits. These are defaults, not a contract — call Merge to
// override.
func DefaultKeymap() Keymap {
	return Keymap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		Left: key.NewBinding(
			key.WithKeys("left", "h"),
			key.WithHelp("←/h", "left"),
		),
		Right: key.NewBinding(
			key.WithKeys("right", "l"),
			key.WithHelp("→/l", "right"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "step in / send"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "step out"),
		),
		Options: key.NewBinding(
			key.WithKeys("o"),
			key.WithHelp("o", "options"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q"),
			key.WithHelp("q", "quit"),
		),
		ForceQuit: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "force quit"),
		),
	}
}

// Override names a single action by field and supplies the keys to rebind it to.
// It is the unit a user-override path passes to Merge. Action is the Keymap
// field name (e.g. "Up", "Options"); an unknown name is ignored by Merge.
type Override struct {
	// Action is the Keymap field to rebind, by its Go field name (case-sensitive:
	// "Up", "Down", "Left", "Right", "Enter", "Back", "Options", "Quit",
	// "ForceQuit").
	Action string
	// Keys are the new key strings for the action (Bubble Tea key names, e.g.
	// "ctrl+n", "g"). The binding keeps its help description.
	Keys []string
}

// Merge layers overrides onto a copy of the receiver and returns the result; the
// receiver is unchanged. Each override rebinds the named action's keys, keeping
// its help text. This is the in-memory user-override path — a later config task
// reads a file and produces the overrides; nothing here does file I/O. An
// override with an empty Action or an unknown field name is skipped.
func (k Keymap) Merge(overrides ...Override) Keymap {
	out := k
	for _, o := range overrides {
		b := out.binding(o.Action)
		if b == nil {
			continue
		}
		b.SetKeys(o.Keys...)
	}
	return out
}

// binding returns a pointer to the named field on the receiver, or nil for an
// unknown name. It is the dispatch table Merge writes through.
func (k *Keymap) binding(action string) *key.Binding {
	switch action {
	case "Up":
		return &k.Up
	case "Down":
		return &k.Down
	case "Left":
		return &k.Left
	case "Right":
		return &k.Right
	case "Enter":
		return &k.Enter
	case "Back":
		return &k.Back
	case "Options":
		return &k.Options
	case "Quit":
		return &k.Quit
	case "ForceQuit":
		return &k.ForceQuit
	default:
		return nil
	}
}
