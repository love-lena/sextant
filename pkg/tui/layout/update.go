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
//   - surface.OpenMsg → open the detail-on-demand pane on the named ref and emit
//     a DetailOpenedMsg as a Cmd, so the host (7.5) can retarget the detail
//     content. The layout handles the mechanics (show + focus + reflow) and stays
//     domain-free about what was opened. The notification is a DISTINCT type, not
//     the raw OpenMsg, so a host that forwards every message back into the layout
//     never re-triggers the open (the loop that a same-type re-emit would cause).
//   - surface.DoneMsg → step focus back to the layout level; if the done pane is
//     the detail pane, hide it and reflow.
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

	case surface.OpenMsg:
		return m.openDetail(msg)

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
// preset cycle, detail toggle, quit) act on the cockpit. A key that matches
// nothing is ignored (it does not fall through to a surface — surfaces only see
// input when active).
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
	case key.Matches(msg, m.keys.Up), key.Matches(msg, m.keys.Left):
		m.moveSelection(-1)
		return m, nil
	case key.Matches(msg, m.keys.Down), key.Matches(msg, m.keys.Right):
		m.moveSelection(+1)
		return m, nil
	case key.Matches(msg, m.keys.DetailToggle):
		return m.toggleDetail()
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
// The step-out binding (Esc) is delivered to the surface AND steps out at the
// layout level. Surfaces consume Esc to tear down their own active state (clear
// a compose buffer) and emit a DoneMsg, the surface-driven step-out path; the
// layout's own step-out is the guaranteed escape for a surface that does not
// consume Esc (e.g. a plain list pane). Both paths land at the layout level, so
// running both is idempotent — the surface's later DoneMsg re-steps-out a no-op.
func (m Model) handlePaneKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if key.Matches(msg, m.keys.ForceQuit) {
		m.Stop()
		return m, tea.Quit
	}
	if m.selected == "" {
		return m, nil
	}
	cmd := m.surfaces[m.selected].Update(msg)
	if key.Matches(msg, m.keys.Back) {
		m.stepOut()
	}
	return m, cmd
}

// moveSelection moves the layout selection by delta through the visible panes in
// registration order, wrapping at the ends. It only runs at the layout level;
// the selected pane gets the accent border.
func (m *Model) moveSelection(delta int) {
	visible := m.visibleOrder()
	if len(visible) == 0 {
		return
	}
	idx := 0
	for i, id := range visible {
		if id == m.selected {
			idx = i
			break
		}
	}
	idx = (idx + delta + len(visible)) % len(visible)
	m.selected = visible[idx]
	m.applyFocus()
}

// stepOut returns focus to the layout level (active pane → selected). Stepping
// out of the detail-on-demand pane also hides it: detail is shown on demand and
// dismissed when the operator leaves it (the btop model — detail is never a
// resting column). This makes the layout-level step-out and a detail surface's
// own DoneMsg converge on the same close behaviour.
func (m *Model) stepOut() {
	if m.selected == detailPaneID && m.hasDetail {
		m.detailShown = false
		m.level = levelLayout
		m.reflow()
		return
	}
	m.level = levelLayout
	m.applyFocus()
}

// toggleVisible turns a pane on or off and reflows so the grid fills the freed
// space (or makes room). The detail pane is never toggled this way — it is
// governed by detail-on-demand (toggleDetail); toggling it here is ignored.
//
// It copies-on-mutate: Update returns Model by value, but hidden is a map (a
// reference), so mutating it in place would retroactively change every earlier
// Model copy still in scope and break Bubble Tea's "a returned model is
// independent" contract. Cloning hidden before the write keeps each returned
// Model's hidden set its own.
func (m Model) toggleVisible(id string) Model {
	if id == detailPaneID {
		return m
	}
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

// toggleDetail shows or hides the detail-on-demand pane and reflows. With detail
// hidden the others reclaim its space; with it shown the grid gives it a slot.
// Showing it also selects it and steps in, so the operator lands on the thing
// they just opened. It is a no-op when no detail surface was supplied.
func (m Model) toggleDetail() (Model, tea.Cmd) {
	if !m.hasDetail {
		return m, nil
	}
	if m.detailShown {
		m.detailShown = false
		if m.selected == detailPaneID {
			m.level = levelLayout
		}
		m.reflow()
		return m, nil
	}
	m.detailShown = true
	// Set the selection + level BEFORE reflowing, so reflow's firstLaidOut guard
	// catches the case where the detail pane was dropped at a tiny terminal: it
	// then demotes the selection to a laid-out pane and drops to the layout level,
	// rather than leaving the selection stepped into a pane that never rendered.
	m.selected = detailPaneID
	m.level = levelPane
	m.reflow()
	return m, nil
}

// DetailOpenedMsg is the layout's host-facing notification that a surface's
// OpenMsg opened the detail-on-demand pane. The layout emits it as a Cmd so the
// host (7.5) can retarget the detail surface's content onto the named ref (e.g.
// load the artifact). It carries the open intent's payload verbatim — the layout
// itself never resolves the ref, staying domain-free.
//
// It is deliberately a DISTINCT type from surface.OpenMsg, not the raw intent:
// the host's Update typically forwards every message into the layout, so a raw
// OpenMsg re-emit would land back in openDetail and re-emit again, spinning
// forever. A distinct notification is inert if forwarded (the layout's Update
// ignores it), so the host may forward-everything safely; it only needs to read
// DetailOpenedMsg to retarget. The layout has already shown + focused the detail
// pane by the time the host sees it, so the host only resolves the ref.
type DetailOpenedMsg struct {
	// Kind is what Ref refers to (mirrors surface.OpenMsg.Kind).
	Kind surface.OpenKind
	// Ref is the reference the host resolves onto the detail surface.
	Ref string
}

// openDetail responds to a surface's OpenMsg: it records the target, shows the
// detail pane (selecting and stepping into it), reflows, and emits a
// DetailOpenedMsg as a Cmd so the host can retarget the detail surface's content.
// The layout never resolves the ref — it stays domain-free; the notification is
// the seam that hands resolution to the host (7.5). Without a detail surface the
// notification still fires (the host may handle it another way) but no pane is
// shown.
func (m Model) openDetail(msg surface.OpenMsg) (Model, tea.Cmd) {
	m.detailTarget = msg.Ref
	notify := func() tea.Msg { return DetailOpenedMsg{Kind: msg.Kind, Ref: msg.Ref} }
	if !m.hasDetail {
		return m, notify
	}
	m.detailShown = true
	// Select + step in BEFORE reflowing so reflow's firstLaidOut guard demotes the
	// selection if the detail pane was dropped at a tiny terminal (see toggleDetail).
	m.selected = detailPaneID
	m.level = levelPane
	m.reflow()
	return m, notify
}

// handleDone responds to a surface's DoneMsg ("I stepped out"): it returns focus
// to the layout level, and if the emitting pane is the detail pane it hides it
// and reflows (detail-on-demand closes cleanly when its surface is done). For
// any other pane it is a plain step-out.
func (m Model) handleDone(msg surface.DoneMsg) (Model, tea.Cmd) {
	if msg.ID == detailPaneID && m.hasDetail {
		m.detailShown = false
		m.level = levelLayout
		m.reflow()
		return m, nil
	}
	m.level = levelLayout
	m.applyFocus()
	return m, nil
}

// setTheme switches the cockpit theme (light/dark) in place. Surfaces resolve
// their hues at construction, so the layout cannot re-theme a mounted surface;
// it switches the chrome it owns (the Box border + title hues + canvas) and
// records the variant in the persisted config. The host re-themes surfaces on
// the next build; in the gallery a fresh build is taken on toggle. This keeps
// the layout domain-free (it never reconstructs a surface).
func (m Model) setTheme(v theme.Variant) Model {
	m.th = theme.New(v)
	m.reflow()
	return m
}

// Theme returns the cockpit's current theme, so a host can rebuild surfaces
// against it after a setTheme (surfaces resolve hues at construction).
func (m Model) Theme() theme.Theme { return m.th }

// Selected returns the id of the currently selected pane (or "" if none). It is
// exposed so a host/test can assert the focus state.
func (m Model) Selected() string { return m.selected }

// SteppedIn reports whether the operator has stepped into a pane (pane level).
func (m Model) SteppedIn() bool { return m.level == levelPane }

// DetailShown reports whether the detail-on-demand pane is currently visible.
func (m Model) DetailShown() bool { return m.detailShown }

// VisibleIDs returns the ids currently laid out, in order — the visible set the
// last reflow arranged. Exposed for tests/hosts that assert the layout.
func (m Model) VisibleIDs() []string { return m.visibleOrder() }

// FocusOf returns a pane's current three-state focus, for tests/hosts asserting
// the focus borders.
func (m Model) FocusOf(id string) widget.Focus { return m.focusOf(id) }
