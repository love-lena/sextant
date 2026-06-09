package layout

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/pkg/tui/surface"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// Update is the cockpit's Bubble Tea update. It is the single place the focus
// machine, intent routing, and reflow triggers live:
//
//   - tea.WindowSizeMsg → record the size and reflow (re-fit every visible pane).
//   - surface.DoneMsg → step focus back to the layout level.
//   - tea.KeyMsg → routed by level: layout-level keys (nav, toggle, preset,
//     options, quit) at levelLayout, the active surface's Update at levelPane.
//
// Any other message (a surface's feed/tick) is broadcast to every mounted
// surface so a background pump keeps running even while another pane is active.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.reflow()
		return m, nil

	case surface.DoneMsg:
		return m.handleDone(msg)

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

// handleKey routes a key by focus level. The options menu, when open, consumes
// keys first.
func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.menu != nil {
		return m.handleMenuKey(msg)
	}
	if m.level == levelPane {
		return m.handlePaneKey(msg)
	}
	return m.handleLayoutKey(msg)
}

// handleLayoutKey handles a key at the layout level: navigation moves the
// selection, Enter steps into the selected pane, the layout keys (options,
// preset cycle, quit) act on the cockpit. A key that matches nothing is ignored
// (it does not fall through to a surface — surfaces only see input when
// active).
func (m Model) handleLayoutKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.ForceQuit):
		m.Stop()
		return m, tea.Quit
	case key.Matches(msg, m.keys.Quit):
		m.Stop()
		return m, tea.Quit
	case key.Matches(msg, m.keys.Options):
		m.menu = newOptionsMenu(m)
		return m, nil
	case key.Matches(msg, m.keys.Enter):
		if m.selected != "" {
			m.level = levelPane
			m.applyFocus()
		}
		return m, nil
	case key.Matches(msg, m.keys.Up):
		m.moveSpatial(dirUp)
		return m, nil
	case key.Matches(msg, m.keys.Down):
		m.moveSpatial(dirDown)
		return m, nil
	case key.Matches(msg, m.keys.Left):
		m.moveSpatial(dirLeft)
		return m, nil
	case key.Matches(msg, m.keys.Right):
		m.moveSpatial(dirRight)
		return m, nil
	case key.Matches(msg, m.keys.PresetCycle):
		m.preset = nextPreset(m.preset)
		m.reflow()
		return m, nil
	}
	return m, nil
}

// handlePaneKey routes a key to the active surface. ForceQuit (Ctrl-C) still
// quits from inside a pane — the root may quit even though a surface must not.
//
// The step-out binding (Esc) is DELIVERED to the surface, never acted on here:
// a surface's active state can be nested (ADR-0024 — a browser's detail opens
// inside its own pane, and each Esc pops exactly one level), so only the
// surface knows whether an Esc pops an inner level or leaves the pane. Leaving
// is the surface's DoneMsg (handleDone steps the layout out); a surface at its
// top level emits one on Back — that is the Surface contract's step-out — and
// ForceQuit stays the guaranteed hard escape.
func (m Model) handlePaneKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if key.Matches(msg, m.keys.ForceQuit) {
		m.Stop()
		return m, tea.Quit
	}
	if m.selected == "" {
		return m, nil
	}
	return m, m.surfaces[m.selected].Update(msg)
}

// direction is a spatial navigation direction at the layout level. Up/Down and
// Left/Right are no longer aliases: each picks the nearest visible pane that lies
// in that direction from the selected pane's rect, so navigation follows the
// cockpit geometry rather than a flat forward/back order.
type direction int

const (
	dirUp direction = iota
	dirDown
	dirLeft
	dirRight
)

// moveSpatial moves the selection to the nearest visible, laid-out pane in dir
// from the selected pane's rect — directional, not a flat forward/back step. A
// candidate qualifies when it lies on the dir side by edges (its near edge is at
// or beyond the selected pane's far edge: the pane immediately to the right /
// below / etc). Among qualifiers it picks the smallest travel-axis gap first,
// then the smallest perpendicular non-overlap, then reading order — so in the
// three-browser cockpit (clients · topics · artifacts side by side), Right from
// clients lands on topics, Right again on artifacts, and Left reverses the walk.
// With no pane in that direction the selection holds (no wrap). It only runs at
// the layout level and never selects a pane without a rect (dropped at a tiny
// terminal), keeping the existing selection guards.
func (m *Model) moveSpatial(dir direction) {
	cur, ok := m.rects[m.selected]
	if !ok {
		// The selection has no rect (none selected, or it was dropped): fall back to
		// the first laid-out pane so a nav key still lands somewhere sensible.
		if first := m.firstLaidOut(); first != "" {
			m.selected = first
			m.applyFocus()
		}
		return
	}

	best := ""
	var bestGap, bestPerp int
	for _, id := range m.visibleOrder() {
		if id == m.selected {
			continue
		}
		r, ok := m.rects[id]
		if !ok {
			continue // never select a pane with no rect
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
		m.selected = best
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

// handleDone responds to a surface's DoneMsg ("I stepped out"): it returns focus
// to the layout level.
func (m Model) handleDone(surface.DoneMsg) (Model, tea.Cmd) {
	m.level = levelLayout
	m.applyFocus()
	return m, nil
}

// setTheme switches the cockpit theme (light/dark) in place. It re-themes the
// chrome it owns (the Box border + title hues + canvas) AND every mounted
// surface, by calling each surface's SetTheme — so the pane BODIES re-theme too,
// not just the chrome. SetTheme is in the Surface contract precisely so the
// layout can re-theme without reconstructing a surface (it stays domain-free —
// it never builds a surface). The variant is recorded in the persisted config so
// the choice survives a relaunch.
func (m Model) setTheme(v theme.Variant) Model {
	m.th = theme.New(v)
	for _, id := range m.order {
		m.surfaces[id].SetTheme(m.th)
	}
	m.reflow()
	return m
}

// Theme returns the cockpit's current theme, so a host/test can read the active
// theme (e.g. to assert a theme toggle took effect).
func (m Model) Theme() theme.Theme { return m.th }

// Selected returns the id of the currently selected pane (or "" if none). It is
// exposed so a host/test can assert the focus state.
func (m Model) Selected() string { return m.selected }

// SteppedIn reports whether the operator has stepped into a pane (pane level).
func (m Model) SteppedIn() bool { return m.level == levelPane }

// VisibleIDs returns the ids currently laid out, in order — the visible set the
// last reflow arranged. Exposed for tests/hosts that assert the layout.
func (m Model) VisibleIDs() []string { return m.visibleOrder() }

// FocusOf returns a pane's current three-state focus, for tests/hosts asserting
// the focus borders.
func (m Model) FocusOf(id string) widget.Focus { return m.focusOf(id) }
