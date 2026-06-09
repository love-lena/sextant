package layout

// Test-only accessors (export_test.go pattern): they expose the unexported rect
// set to the external layout_test package so a test can assert against what the
// layout ACTUALLY laid out, not what View happens to echo. View paints the
// selected pane id into the status bar, so "is this id in View" is not a sound
// proxy for "did this pane get a rect" — these read m.rects directly instead.

// LaidOut reports whether a pane id got a rect in the last reflow — i.e. it is
// visible AND it fit (not dropped at a tiny terminal). This is the ground truth
// the canvas renders from, independent of any selection echo in the chrome.
func (m Model) LaidOut(id string) bool {
	_, ok := m.rects[id]
	return ok
}

// RectXYWH returns a laid-out pane's outer rect (origin + size), so a spatial-nav
// test can assert the geometry it navigates against rather than hardcoding it.
// The bool is false when the pane has no rect.
func (m Model) RectXYWH(id string) (x, y, w, h int, ok bool) {
	r, ok := m.rects[id]
	return r.X, r.Y, r.W, r.H, ok
}

// MenuOpen reports whether the options menu overlay is open — the input state
// behind which the paste guard must still hold (a pasted chunk must not match
// the menu's bindings either).
func (m Model) MenuOpen() bool { return m.menu != nil }
