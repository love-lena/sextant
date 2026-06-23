package layout

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/clients/go/apps/internal/tui/theme"
	"github.com/love-lena/sextant/clients/go/apps/internal/tui/widget"
)

// Update is the cockpit's Bubble Tea update. It is the single place focus
// movement, intent routing, and reflow triggers live:
//
//   - tea.WindowSizeMsg → record the size and reflow (re-fit every visible pane).
//   - tea.KeyMsg → the layout intercepts only the focus-movement keys, quit, and
//     its own chrome keys (preset cycle, options menu); every other key goes to
//     the focused surface (ADR-0026: keys always go to the focused pane).
//
// Any other message (a surface's feed/tick) is broadcast to every mounted
// surface so a background pump keeps running regardless of focus.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.reflow()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// A non-key message (a feed event, a refresh tick, a publish result): broadcast
	// to every surface so background work continues regardless of focus.
	return m, m.broadcast(msg)
}

// broadcast routes a message to every mounted surface and batches their
// follow-up commands. Surfaces own their own message types, so a message
// meant for one is a no-op in the others.
func (m Model) broadcast(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for _, id := range m.order {
		if c := m.surfaces[id].Update(msg); c != nil {
			cmds = append(cmds, c)
		}
	}
	return tea.Batch(cmds...)
}

// handleKey routes a key under the one-focused-pane model (ADR-0026). The
// options menu, when open, consumes keys first. Then the layout claims only:
//
//   - ForceQuit (Ctrl-C) — always quits, from any state.
//   - the focus-movement keys (cycle + spatial) — modifier/Tab keys a surface
//     never claims, so they work mid-list, mid-conversation, mid-compose.
//   - the chrome keys (Quit, Options, PresetCycle) — but ONLY while the focused
//     surface is not capturing text. While a compose is capturing, a printable
//     key types (q types a q); fail-safe is to deliver the key to the surface
//     rather than act underneath typing.
//
// Everything else is delivered to the focused surface. Focus movement never
// touches a surface's content state — an open detail stays open.
func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	// A burst/pasted chunk is TEXT, never a chord (widget.IsTextChunk: a
	// bracketed paste — even a single character, which would otherwise be
	// indistinguishable from the keystroke — or a multi-rune KeyRunes burst).
	// Its String() can spell a binding name ("ctrl+c", "tab"), and binding
	// matches compare strings, so matching it against the bindings would let
	// pasted text quit the dash or move focus. Text is content, in EVERY input
	// state — the guard sits above the menu dispatch because the menu matches
	// bindings too (pasting "ctrl+c" over an open menu must not quit). With the
	// menu open there is no text consumer, so the chunk is dropped rather than
	// delivered behind the menu.
	if widget.IsTextChunk(msg) {
		if m.menu != nil || m.focused == "" {
			return m, nil
		}
		return m, m.surfaces[m.focused].Update(msg)
	}
	if m.menu != nil {
		return m.handleMenuKey(msg)
	}
	switch {
	case key.Matches(msg, m.keys.ForceQuit):
		m.Stop()
		return m, tea.Quit
	case key.Matches(msg, m.keys.FocusNext):
		m.cycleFocus(1)
		return m, nil
	case key.Matches(msg, m.keys.FocusPrev):
		m.cycleFocus(-1)
		return m, nil
	case key.Matches(msg, m.keys.FocusLeft):
		m.moveSpatial(dirLeft)
		return m, nil
	case key.Matches(msg, m.keys.FocusDown):
		m.moveSpatial(dirDown)
		return m, nil
	case key.Matches(msg, m.keys.FocusUp):
		m.moveSpatial(dirUp)
		return m, nil
	case key.Matches(msg, m.keys.FocusRight):
		m.moveSpatial(dirRight)
		return m, nil
	}

	if !m.focusedCapturing() {
		switch {
		case key.Matches(msg, m.keys.Quit):
			m.Stop()
			return m, tea.Quit
		case key.Matches(msg, m.keys.Options):
			m.menu = newOptionsMenu(m)
			return m, nil
		case key.Matches(msg, m.keys.PresetCycle):
			m.preset = nextPreset(m.preset)
			m.reflow()
			return m, nil
		}
	}

	if m.focused == "" {
		return m, nil
	}
	return m, m.surfaces[m.focused].Update(msg)
}

// focusedCapturing reports whether the focused surface is capturing text (the
// Surface contract's CapturingText). With no focused pane it is false, so the
// chrome keys still work at a degraded (too-small) terminal.
func (m Model) focusedCapturing() bool {
	if m.focused == "" {
		return false
	}
	return m.surfaces[m.focused].CapturingText()
}

// cycleFocus moves focus step panes forward (+1) or back (-1) through the
// laid-out panes in registration order, wrapping at the ends. With nothing
// laid out it is a no-op; with the focus somehow off a laid-out pane it lands
// on the first one.
func (m *Model) cycleFocus(step int) {
	ids := m.laidOutOrder()
	if len(ids) == 0 {
		return
	}
	cur := -1
	for i, id := range ids {
		if id == m.focused {
			cur = i
		}
	}
	if cur < 0 {
		m.focused = ids[0]
	} else {
		n := len(ids)
		m.focused = ids[(cur+step+n)%n]
	}
	m.applyFocus()
}

// laidOutOrder returns the visible panes that actually got a rect from the last
// reflow, in registration order — the panes focus can land on.
func (m Model) laidOutOrder() []string {
	var out []string
	for _, id := range m.visibleOrder() {
		if _, ok := m.rects[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

// direction is a spatial focus-movement direction. Up/Down and Left/Right are
// not aliases: each picks the nearest visible pane that lies in that direction
// from the focused pane's rect, so movement follows the cockpit geometry rather
// than a flat forward/back order.
type direction int

const (
	dirUp direction = iota
	dirDown
	dirLeft
	dirRight
)

// moveSpatial moves focus to the nearest visible, laid-out pane in dir from the
// focused pane's rect — directional, not a flat forward/back step. A candidate
// qualifies when it lies on the dir side by edges (its near edge is at or beyond
// the focused pane's far edge: the pane immediately to the right / below / etc).
// Among qualifiers it picks the smallest travel-axis gap first, then the
// smallest perpendicular non-overlap, then reading order — so in the
// three-browser cockpit (clients · topics · artifacts side by side), Ctrl+l from
// clients lands on topics, Ctrl+l again on artifacts, and Ctrl+h reverses the
// walk. With no pane in that direction the focus holds (no wrap; the cycle keys
// wrap instead). It never focuses a pane without a rect (dropped at a tiny
// terminal). Moving focus never touches a surface's content state.
func (m *Model) moveSpatial(dir direction) {
	cur, ok := m.rects[m.focused]
	if !ok {
		// The focus has no rect (none focused, or it was dropped): fall back to
		// the first laid-out pane so a focus key still lands somewhere sensible.
		if first := m.firstLaidOut(); first != "" {
			m.focused = first
			m.applyFocus()
		}
		return
	}

	best := ""
	var bestGap, bestPerp int
	for _, id := range m.visibleOrder() {
		if id == m.focused {
			continue
		}
		r, ok := m.rects[id]
		if !ok {
			continue // never focus a pane with no rect
		}
		gap, ok := travelGap(dir, cur, r)
		if !ok {
			continue // not on the dir side
		}
		perp := perpGap(dir, cur, r)
		// visibleOrder is reading order, so the FIRST qualifier at a given
		// (gap, perp) wins the tie — the topmost/leftmost pane among equals.
		if best == "" || gap < bestGap || (gap == bestGap && perp < bestPerp) {
			best, bestGap, bestPerp = id, gap, perp
		}
	}
	if best != "" {
		m.focused = best
		m.applyFocus()
	}
}

// travelGap returns the gap along the travel axis from cur's far edge to r's near
// edge, and whether r lies on the dir side at all. r qualifies when its near edge
// is at or beyond cur's far edge (so a directly adjacent pane has gap 0). A pane
// behind or overlapping cur on the travel axis does not qualify.
func travelGap(dir direction, cur, r Rect) (int, bool) {
	switch dir {
	case dirUp:
		gap := cur.Y - (r.Y + r.H)
		return gap, r.Y+r.H <= cur.Y
	case dirDown:
		gap := r.Y - (cur.Y + cur.H)
		return gap, r.Y >= cur.Y+cur.H
	case dirLeft:
		gap := cur.X - (r.X + r.W)
		return gap, r.X+r.W <= cur.X
	default: // dirRight
		gap := r.X - (cur.X + cur.W)
		return gap, r.X >= cur.X+cur.W
	}
}

// perpGap returns how far r sits off the travel axis from cur: the distance
// between their spans on the perpendicular axis, 0 when they overlap. It is the
// tie-break after the travel gap, so the most-aligned pane in dir wins.
func perpGap(dir direction, cur, r Rect) int {
	switch dir {
	case dirUp, dirDown:
		return spanGap(cur.X, cur.X+cur.W, r.X, r.X+r.W)
	default: // dirLeft, dirRight
		return spanGap(cur.Y, cur.Y+cur.H, r.Y, r.Y+r.H)
	}
}

// spanGap returns the gap between two 1-D spans [a0,a1) and [b0,b1): 0 when they
// overlap, otherwise the distance between the nearer edges.
func spanGap(a0, a1, b0, b1 int) int {
	if b0 >= a1 {
		return b0 - a1
	}
	if a0 >= b1 {
		return a0 - b1
	}
	return 0
}

// toggleVisible turns a pane on or off and reflows so the grid fills the freed
// space (or makes room).
//
// It copies-on-mutate: Update returns Model by value, but hidden is a map (a
// reference), so mutating it in place would retroactively change every earlier
// Model copy still in scope and break Bubble Tea's "a returned model is
// independent" contract. Cloning hidden before the write keeps each returned
// Model's hidden set its own.
func (m Model) toggleVisible(id string) Model {
	if _, ok := m.surfaces[id]; !ok {
		return m
	}
	m.hidden = cloneHidden(m.hidden)
	if m.hidden[id] {
		delete(m.hidden, id)
	} else {
		m.hidden[id] = true
	}
	m.reflow()
	return m
}

// cloneHidden returns an independent copy of a hidden set, so a mutation on the
// copy never aliases back into a Model value that shared the original map.
func cloneHidden(src map[string]bool) map[string]bool {
	dst := make(map[string]bool, len(src))
	for id, h := range src {
		dst[id] = h
	}
	return dst
}

// setTheme switches the cockpit theme (light/dark) in place. It re-themes the
// chrome it owns (the Box border + title hues + canvas) AND every mounted
// surface, by calling each surface's SetTheme — so the pane BODIES re-theme too,
// not just the chrome. SetTheme is in the Surface contract precisely so the
// layout can re-theme without reconstructing a surface (it stays domain-free —
// it never builds a surface). The variant is recorded as the persisted theme
// choice so it survives a relaunch — toggling out of auto picks a concrete
// theme and stays concrete until the operator asks for auto again (--theme
// auto).
func (m Model) setTheme(v theme.Variant) Model {
	m.th = theme.New(v)
	m.themeChoice = m.th.Variant
	for _, id := range m.order {
		m.surfaces[id].SetTheme(m.th)
	}
	m.reflow()
	return m
}

// Theme returns the cockpit's current theme, so a host/test can read the active
// theme (e.g. to assert a theme toggle took effect).
func (m Model) Theme() theme.Theme { return m.th }

// Focused returns the id of the currently focused pane (or "" if none). It is
// exposed so a host/test can assert the focus state.
func (m Model) Focused() string { return m.focused }

// VisibleIDs returns the ids currently laid out, in order — the visible set the
// last reflow arranged. Exposed for tests/hosts that assert the layout.
func (m Model) VisibleIDs() []string { return m.visibleOrder() }

// FocusOf returns a pane's current three-state focus, for tests/hosts asserting
// the focus borders: active for the focused pane, selected for other visible
// panes, idle for hidden ones.
func (m Model) FocusOf(id string) widget.Focus {
	if !m.isVisible(id) {
		return widget.FocusIdle
	}
	return m.focusOf(id)
}
