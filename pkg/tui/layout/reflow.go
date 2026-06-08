package layout

import "github.com/love-lena/sextant/pkg/tui/widget"

// Box overhead: a Box draws 2 border columns + 2 padding columns wide and 2
// border rows tall, so a surface's inner content area is (w-4, h-2). This is the
// same convention the widgets, surfaces, and galleries use; the layout sizes
// each surface to it on every reflow.
const (
	boxOverheadW = 4
	boxOverheadH = 2
)

// visibleOrder returns the ids to lay out, in the host's registration order: the
// panes that are not hidden, plus the detail pane only when it is shown (and a
// detail surface exists). The detail pane is appended last so a preset puts it
// in a trailing slot (the cockpit/stream secondary stack), never an always-on
// leading column.
func (m Model) visibleOrder() []string {
	var out []string
	for _, id := range m.order {
		if id == detailPaneID {
			continue // detail is governed by detailShown, handled below
		}
		if !m.hidden[id] {
			out = append(out, id)
		}
	}
	if m.hasDetail && m.detailShown {
		out = append(out, detailPaneID)
	}
	return out
}

// firstVisible returns the first visible pane id in registration order, or "" if
// none are visible. It is the fallback selection when the current selection is
// hidden or the layout has just been built.
func (m Model) firstVisible() string {
	if v := m.visibleOrder(); len(v) > 0 {
		return v[0]
	}
	return ""
}

// isVisible reports whether a pane id is currently in the visible set.
func (m Model) isVisible(id string) bool {
	for _, v := range m.visibleOrder() {
		if v == id {
			return true
		}
	}
	return false
}

// firstLaidOut returns the first visible pane that actually got a rect from the
// last reflow, in registration order — the panes the operator can really land
// on. It differs from firstVisible only at a tiny terminal, where arrange drops
// the visible panes that don't fit; the selection must follow what is rendered,
// not the nominal visible set.
func (m Model) firstLaidOut() string {
	for _, id := range m.visibleOrder() {
		if _, ok := m.rects[id]; ok {
			return id
		}
	}
	return ""
}

// reflow recomputes the arrangement and resizes every surface to its box inner
// area. It is the one place geometry is applied: called on a resize, a pane
// toggle, a preset switch, and a detail show/hide. It (1) computes the visible
// set, (2) arranges it into outer Rects for the current size, (3) sizes each
// visible surface to its box inner area, and (4) keeps the selection valid —
// if the selected pane went hidden, the selection snaps to the first visible
// pane and focus drops to the layout level. Hidden surfaces are sized to zero so
// a later toggle-on re-sizes them on reflow (they hold no live geometry while
// off).
func (m *Model) reflow() {
	if m.w <= 0 || m.h <= 0 {
		return
	}
	visible := m.visibleOrder()
	m.rects = arrange(m.preset, visible, m.w, m.areaH())

	for _, id := range m.order {
		s := m.surfaces[id]
		r, shown := m.rects[id]
		if !shown {
			continue
		}
		iw, ih := innerSize(r.W, r.H)
		s.SetSize(iw, ih)
	}

	// Keep the selection on a pane that was actually laid out. A pane can be in the
	// visible set yet dropped by arrange at a tiny terminal, so check the rects, not
	// just visibility.
	if _, ok := m.rects[m.selected]; !ok {
		m.selected = m.firstLaidOut()
		m.level = levelLayout
	}
	m.applyFocus()
}

// innerSize converts an outer box rectangle to the inner content area a surface
// is sized to, clamped to at least 1×1 so a tiny cell never produces a negative
// size.
func innerSize(w, h int) (int, int) {
	iw, ih := w-boxOverheadW, h-boxOverheadH
	if iw < 1 {
		iw = 1
	}
	if ih < 1 {
		ih = 1
	}
	return iw, ih
}

// applyFocus pushes each surface's three-state focus from the layout state, so
// only the selected pane shows an accent border and only the stepped-in pane
// lights its inner cursor. A hidden surface is set idle.
func (m *Model) applyFocus() {
	for _, id := range m.order {
		f := widget.FocusIdle
		if m.isVisible(id) {
			f = m.focusOf(id)
		}
		m.surfaces[id].SetFocus(f)
	}
}
