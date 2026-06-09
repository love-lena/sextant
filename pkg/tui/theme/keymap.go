package theme

import "github.com/charmbracelet/bubbles/key"

// Keymap is the dash's binding set as data: one named key.Binding per action.
// Nothing in a widget hardcodes a key — widgets read bindings from a Keymap, so
// the bindings are overridable defaults, not a contract. The default reflects
// the one-focused-pane model (ADR-0026): the focus-movement keys move focus
// between panes and are never claimed by a surface; every other binding is a
// content key delivered to the focused surface (arrows move within, Enter opens
// or sends, Esc pops one level).
//
// Keys are expected to churn. Treat DefaultKeymap as a starting point and use
// Merge to layer a user's overrides on top.
type Keymap struct {
	// Up and Down move the cursor / scroll within the focused pane.
	Up   key.Binding
	Down key.Binding
	// Left and Right move across columns within the focused pane.
	Left  key.Binding
	Right key.Binding

	// Enter opens the selected row's detail in place (in a browser) or sends the
	// composed message (in a conversation).
	Enter key.Binding
	// Back pops one level within the focused pane (detail → list). It never
	// quits and never moves focus; Esc at a pane's top level does nothing.
	Back key.Binding

	// FocusNext and FocusPrev cycle focus through the visible panes (ADR-0026).
	// They are layout keys a surface never claims, so they work from any state —
	// mid-list, mid-conversation, mid-compose.
	FocusNext key.Binding
	FocusPrev key.Binding
	// FocusLeft / FocusDown / FocusUp / FocusRight move focus spatially to the
	// nearest visible pane in that direction, vim-style. Layout keys, like the
	// cycle pair. Ctrl+h shares a byte with Backspace on legacy terminals
	// (modern terminals send DEL for Backspace, so the two are distinct keys);
	// an operator on a terminal with the legacy mapping rebinds FocusLeft.
	FocusLeft  key.Binding
	FocusDown  key.Binding
	FocusUp    key.Binding
	FocusRight key.Binding

	// Options opens the universal options menu.
	Options key.Binding

	// PresetCycle advances to the next built-in preset layout. It is
	// layout-only — surfaces never see it — but lives here so it is an
	// overridable default like every other binding, not a hardcoded key.
	PresetCycle key.Binding

	// Quit leaves the dash cleanly. It acts only while the focused surface is
	// not capturing text; while a compose is capturing, the key types instead.
	Quit key.Binding
	// ForceQuit is the hard quit (Ctrl-C). It always quits, from any state.
	ForceQuit key.Binding
}

// DefaultKeymap returns the locked default binding set (ADR-0026). Arrows and
// hjkl navigate within the focused pane; Enter opens/sends; Esc pops one level;
// Tab/Shift+Tab cycle focus; Ctrl+h/j/k/l move focus spatially; o opens
// options; p cycles the preset; q quits (when not composing) and Ctrl-C
// hard-quits. These are defaults, not a contract — call Merge to override.
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
			key.WithHelp("enter", "open / send"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),
		FocusNext: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "focus next pane"),
		),
		FocusPrev: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "focus previous pane"),
		),
		FocusLeft: key.NewBinding(
			key.WithKeys("ctrl+h"),
			key.WithHelp("ctrl+h", "focus left"),
		),
		FocusDown: key.NewBinding(
			key.WithKeys("ctrl+j"),
			key.WithHelp("ctrl+j", "focus down"),
		),
		FocusUp: key.NewBinding(
			key.WithKeys("ctrl+k"),
			key.WithHelp("ctrl+k", "focus up"),
		),
		FocusRight: key.NewBinding(
			key.WithKeys("ctrl+l"),
			key.WithHelp("ctrl+l", "focus right"),
		),
		Options: key.NewBinding(
			key.WithKeys("o"),
			key.WithHelp("o", "options"),
		),
		PresetCycle: key.NewBinding(
			key.WithKeys("p"),
			key.WithHelp("p", "cycle preset"),
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
	// "Up", "Down", "Left", "Right", "Enter", "Back", "FocusNext", "FocusPrev",
	// "FocusLeft", "FocusDown", "FocusUp", "FocusRight", "Options",
	// "PresetCycle", "Quit", "ForceQuit").
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
	case "FocusNext":
		return &k.FocusNext
	case "FocusPrev":
		return &k.FocusPrev
	case "FocusLeft":
		return &k.FocusLeft
	case "FocusDown":
		return &k.FocusDown
	case "FocusUp":
		return &k.FocusUp
	case "FocusRight":
		return &k.FocusRight
	case "Options":
		return &k.Options
	case "PresetCycle":
		return &k.PresetCycle
	case "Quit":
		return &k.Quit
	case "ForceQuit":
		return &k.ForceQuit
	default:
		return nil
	}
}
