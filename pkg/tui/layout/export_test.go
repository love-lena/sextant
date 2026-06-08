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
