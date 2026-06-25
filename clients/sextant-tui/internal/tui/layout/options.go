package layout

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/theme"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/widget"
)

// optionsMenu is the universal options overlay (ADR-0023): a small selectable
// list to toggle panes on/off, switch preset, switch theme, and quit. It is
// deliberately minimal — a cursor over a fixed set of action rows, not a settings
// framework. It opens on the keymap's Options binding (`o`) and closes on Back
// (Esc); Enter activates the cursor row. Each row's effect is applied to the
// layout, which reflows.
type optionsMenu struct {
	cursor int
	items  []optionItem
}

// optionKind classifies an option row so activation knows what to do without
// parsing a label.
type optionKind int

const (
	optTogglePane optionKind = iota
	optSwitchPreset
	optSwitchTheme
	optQuit
)

// optionItem is one row in the menu: a label, a kind, a target (the pane id
// for a toggle row; unused otherwise), and — for toggle rows — the current
// on/off state so the overlay can colour the state tag.
type optionItem struct {
	kind   optionKind
	label  string
	paneID string
	on     bool // true when the pane is visible; only meaningful for optTogglePane
}

// newOptionsMenu builds the menu for the current layout state: one toggle row
// per pane, a preset-switch row, a theme-switch row, and quit. Toggle rows
// carry the on/off state separately so the overlay can colour the state tag.
func newOptionsMenu(m Model) *optionsMenu {
	var items []optionItem
	for _, id := range m.order {
		visible := !m.hidden[id]
		items = append(items, optionItem{
			kind:   optTogglePane,
			label:  "pane " + id,
			paneID: id,
			on:     visible,
		})
	}
	items = append(
		items,
		optionItem{kind: optSwitchPreset, label: "preset: " + m.preset + " →"},
		optionItem{kind: optSwitchTheme, label: "theme: " + string(m.th.Variant) + " ↔"},
		optionItem{kind: optQuit, label: "quit"},
	)
	return &optionsMenu{items: items}
}

// handleMenuKey routes a key while the options menu is open: navigation moves
// the cursor, Enter activates the row, Back/Options closes the menu, ForceQuit
// quits.
func (m Model) handleMenuKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.ForceQuit):
		m.Stop()
		return m, tea.Quit
	case key.Matches(msg, m.keys.Back), key.Matches(msg, m.keys.Options):
		m.menu = nil
		return m, nil
	case key.Matches(msg, m.keys.Up):
		m.menu.cursor = (m.menu.cursor - 1 + len(m.menu.items)) % len(m.menu.items)
		return m, nil
	case key.Matches(msg, m.keys.Down):
		m.menu.cursor = (m.menu.cursor + 1) % len(m.menu.items)
		return m, nil
	case key.Matches(msg, m.keys.Enter):
		return m.activateOption()
	}
	return m, nil
}

// activateOption applies the cursor row's effect. Toggling a pane or switching
// preset reflows; switching theme re-themes the chrome; quit tears down and
// quits. The menu stays open after a toggle/preset/theme change (so the operator
// can flip several), and the labels are rebuilt to reflect the new state.
func (m Model) activateOption() (Model, tea.Cmd) {
	it := m.menu.items[m.menu.cursor]
	cursor := m.menu.cursor
	switch it.kind {
	case optTogglePane:
		m = m.toggleVisible(it.paneID)
	case optSwitchPreset:
		m.preset = nextPreset(m.preset)
		m.reflow()
	case optSwitchTheme:
		if m.th.Variant == theme.VariantLight {
			m = m.setTheme(theme.VariantDark)
		} else {
			m = m.setTheme(theme.VariantLight)
		}
	case optQuit:
		m.Stop()
		return m, tea.Quit
	}
	// Rebuild the menu so labels reflect the new state, keeping the cursor row.
	m.menu = newOptionsMenu(m)
	if cursor < len(m.menu.items) {
		m.menu.cursor = cursor
	}
	return m, nil
}

// overlay composites the menu panel over the live cockpit render, so the
// operator sees the cockpit behind it (and the effect of a toggle live). The
// panel is a widget.Box titled "options" holding the selectable rows, centred on
// the cockpit area; it is spliced onto a canvas of the behind render so the
// background panes show around it.
//
// Toggle rows render the [on]/[off] state tag in a state-bearing hue: green
// (affirmative) for on, dim for off — so the flip is unmissable in both themes.
func (o *optionsMenu) overlay(t theme.Theme, behind string, w, h int) string {
	// affirmativeHue is the "on" colour: green (base0B), the connected/active slot.
	affirmativeHue := lipgloss.Color(t.Palette.Base0B)

	var b strings.Builder
	for i, it := range o.items {
		isCursor := i == o.cursor
		rowStyle := lipgloss.NewStyle().Foreground(t.Fg)
		if isCursor {
			rowStyle = lipgloss.NewStyle().Background(t.Accent).Foreground(t.OnAccent).Bold(true)
		}

		if it.kind == optTogglePane {
			// Render the state tag in a state-bearing hue, outside the cursor bar
			// style, so it reads clearly in both cursor and resting states.
			var tagStyle lipgloss.Style
			var tag string
			if it.on {
				tag = "[on ]"
				tagStyle = lipgloss.NewStyle().Foreground(affirmativeHue)
			} else {
				tag = "[off]"
				tagStyle = lipgloss.NewStyle().Foreground(t.Dim)
			}
			if isCursor {
				// On the cursor row the entire background is the accent bar; keep
				// the tag colours readable against that background.
				tagStyle = tagStyle.Background(t.Accent)
			}
			b.WriteString(rowStyle.Render(" " + it.label + " "))
			b.WriteString(tagStyle.Render(tag + " "))
		} else {
			b.WriteString(rowStyle.Render(" " + it.label + " "))
		}
		if i < len(o.items)-1 {
			b.WriteByte('\n')
		}
	}

	// Row width: for toggle rows label + " " + tag (5) + 2 padding spaces = label+8;
	// for other rows label + 2 padding spaces.
	innerW := 0
	for _, it := range o.items {
		var rowW int
		if it.kind == optTogglePane {
			rowW = lipgloss.Width(it.label) + 8 // " " + label + " " + "[on ]" + " "
		} else {
			rowW = lipgloss.Width(it.label) + 2 // " " + label + " "
		}
		if rowW > innerW {
			innerW = rowW
		}
	}
	if innerW < 16 {
		innerW = 16
	}
	boxW := innerW + boxOverheadW
	boxH := len(o.items) + boxOverheadH
	panel := widget.Box(t, widget.FocusActive, "options", t.Accent, b.String(), boxW, boxH)

	// Composite the panel centred over the cockpit render: rebuild a canvas from the
	// behind rows and splice the panel into the middle, so the cockpit stays visible
	// around the menu.
	rows := strings.Split(behind, "\n")
	c := &canvas{w: w, h: len(rows), rows: rows}
	x := max(0, (w-boxW)/2)
	y := max(0, (len(rows)-boxH)/2)
	c.place(panel, x, y)
	return c.render()
}
