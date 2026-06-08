package layout_test

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/love-lena/sextant/pkg/tui/layout"
	"github.com/love-lena/sextant/pkg/tui/surface"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// stripANSI removes escape sequences so a test can assert on the plain text of a
// render.
func stripANSI(s string) string { return ansi.Strip(s) }

// key builds a tea.KeyMsg for a single rune or named key, the way bubbletea
// delivers it. It lets the model tests drive the focus machine through the same
// path a live program does.
func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// newCockpit builds a Model over three plain panes plus a detail pane, sized to
// a fixed terminal, ready for the focus/reflow assertions.
func newCockpit(t *testing.T) (layout.Model, map[string]*mockSurface) {
	t.Helper()
	panes := map[string]*mockSurface{
		"presence": newMock("presence", "presence"),
		"stream":   newMock("stream", "stream"),
		"artifact": newMock("artifact", "artifact"),
		"detail":   newMock("detail", "detail"),
	}
	m := layout.New(theme.Dark(), theme.DefaultKeymap(), layout.DefaultConfig(),
		panes["presence"], panes["stream"], panes["artifact"], panes["detail"])
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m, panes
}

// TestStartsAtLayoutLevel: a fresh cockpit selects the first visible pane, is at
// the layout level (not stepped in), and the detail pane is hidden by default.
func TestStartsAtLayoutLevel(t *testing.T) {
	m, _ := newCockpit(t)
	if m.Selected() != "presence" {
		t.Errorf("first selection = %q, want presence", m.Selected())
	}
	if m.SteppedIn() {
		t.Error("should start at layout level, not stepped in")
	}
	if m.DetailShown() {
		t.Error("detail should be hidden by default")
	}
	if got := m.VisibleIDs(); !reflect.DeepEqual(got, []string{"presence", "stream", "artifact"}) {
		t.Errorf("visible = %v, want presence/stream/artifact (no detail)", got)
	}
}

// TestNavMovesSelection: nav keys move the selected pane through the visible set,
// the selected pane carries the accent (selected) border, the others idle.
func TestNavMovesSelection(t *testing.T) {
	m, _ := newCockpit(t)
	if m.FocusOf("presence") != widget.FocusSelected {
		t.Errorf("presence focus = %v, want selected", m.FocusOf("presence"))
	}
	m, _ = m.Update(key("down"))
	if m.Selected() != "stream" {
		t.Errorf("after down, selected = %q, want stream", m.Selected())
	}
	if m.FocusOf("presence") != widget.FocusIdle {
		t.Errorf("presence should be idle after moving off it, got %v", m.FocusOf("presence"))
	}
	if m.FocusOf("stream") != widget.FocusSelected {
		t.Errorf("stream focus = %v, want selected", m.FocusOf("stream"))
	}
}

// TestStepInOutWithKeys: Enter steps into the selected pane (it goes active and
// receives input), Esc steps back out (selected). This is the locked two-level
// model.
func TestStepInOutWithKeys(t *testing.T) {
	m, panes := newCockpit(t)
	m, _ = m.Update(key("enter"))
	if !m.SteppedIn() {
		t.Fatal("Enter should step in")
	}
	if m.FocusOf("presence") != widget.FocusActive {
		t.Errorf("stepped-in pane focus = %v, want active", m.FocusOf("presence"))
	}
	if panes["presence"].focus != widget.FocusActive {
		t.Errorf("surface was not told it is active: %v", panes["presence"].focus)
	}
	m, _ = m.Update(key("esc"))
	if m.SteppedIn() {
		t.Error("Esc should step out")
	}
	if m.FocusOf("presence") != widget.FocusSelected {
		t.Errorf("stepped-out pane focus = %v, want selected", m.FocusOf("presence"))
	}
}

// TestStepInRoutesKeysToActiveSurface: while stepped in, a key reaches the active
// surface's Update (and not the others).
func TestStepInRoutesKeysToActiveSurface(t *testing.T) {
	m, panes := newCockpit(t)
	// Make the stream emit DoneMsg on Esc so we can observe a routed key.
	panes["stream"].onEsc = true
	m, _ = m.Update(key("down")) // select stream
	m, _ = m.Update(key("enter"))
	if !m.SteppedIn() {
		t.Fatal("should be stepped into stream")
	}
	// Esc while active routes to the surface, which emits DoneMsg; feeding that back
	// steps out.
	_, cmd := m.Update(key("esc"))
	if cmd == nil {
		t.Fatal("active Esc should route to surface and produce its DoneMsg cmd")
	}
	msg := cmd()
	done, ok := msg.(surface.DoneMsg)
	if !ok || done.ID != "stream" {
		t.Fatalf("expected DoneMsg{stream}, got %#v", msg)
	}
}

// TestDoneMsgStepsOut: a surface's own DoneMsg returns focus to the layout level.
func TestDoneMsgStepsOut(t *testing.T) {
	m, _ := newCockpit(t)
	m, _ = m.Update(key("enter"))
	if !m.SteppedIn() {
		t.Fatal("precondition: stepped in")
	}
	m, _ = m.Update(surface.DoneMsg{ID: "presence"})
	if m.SteppedIn() {
		t.Error("DoneMsg should step out to the layout level")
	}
}

// totalInner sums the inner area each visible surface was sized to, so a test
// can assert reflow conserves (and reclaims) the terminal area without pinning a
// specific preset's geometry.
func totalInner(panes map[string]*mockSurface, visible []string) int {
	sum := 0
	for _, id := range visible {
		p := panes[id]
		sum += p.w * p.h
	}
	return sum
}

// TestToggleReflowsToFill: hiding a pane removes it from the visible set and the
// remaining panes reflow to fill the freed space. The total inner area the
// surfaces are sized to grows (the freed pane's space is reclaimed, not left as
// a gap). Showing it again restores the original layout.
func TestToggleReflowsToFill(t *testing.T) {
	m, panes := newCockpit(t)
	beforeVisible := m.VisibleIDs()
	beforeArea := totalInner(panes, beforeVisible)

	m, _ = m.Update(key("o")) // open options
	// Cursor row 0 is "pane presence"; Enter toggles it off.
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("esc")) // close menu

	afterVisible := m.VisibleIDs()
	if contains(afterVisible, "presence") {
		t.Errorf("presence should be hidden, visible = %v", afterVisible)
	}
	afterArea := totalInner(panes, afterVisible)
	// Fewer panes over the same terminal → less box overhead → MORE inner area for
	// the survivors. The freed space is reclaimed, never left as a gap.
	if afterArea <= beforeArea {
		t.Errorf("reflow did not reclaim freed space: remaining inner area %d not > %d", afterArea, beforeArea)
	}

	// Toggle presence back on via the menu.
	m, _ = m.Update(key("o"))
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("esc"))
	if got := m.VisibleIDs(); !reflect.DeepEqual(got, beforeVisible) {
		t.Errorf("toggling back should restore the layout: got %v, want %v", got, beforeVisible)
	}
}

// TestResizeReflows: a WindowSizeMsg re-fits every visible surface to the new
// terminal size.
func TestResizeReflows(t *testing.T) {
	m, panes := newCockpit(t)
	w1 := panes["presence"].w
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	w2 := panes["presence"].w
	if w2 <= w1 {
		t.Errorf("resize did not re-fit: presence width %d not > %d", w2, w1)
	}
}

// TestToggleDoesNotAliasEarlierModel proves the copy-on-mutate fix: Update
// returns Model by value, and toggling a pane on a derived copy must NOT
// retroactively change an earlier Model value still in scope (the hidden map
// used to alias across copies). A snapshotting host/test relies on this Bubble
// Tea contract.
func TestToggleDoesNotAliasEarlierModel(t *testing.T) {
	before, _ := newCockpit(t)
	beforeVisible := append([]string(nil), before.VisibleIDs()...)

	// Toggle a pane off on a derived copy (via the options menu, the real toggle path).
	after, _ := before.Update(key("o"))
	after, _ = after.Update(key("down")) // move to "pane stream"
	after, _ = after.Update(key("enter"))
	after, _ = after.Update(key("esc"))

	if contains(after.VisibleIDs(), "stream") {
		t.Fatalf("precondition: stream should be hidden on the derived copy, got %v", after.VisibleIDs())
	}
	// The earlier Model must be untouched.
	if got := before.VisibleIDs(); !reflect.DeepEqual(got, beforeVisible) {
		t.Errorf("toggling on a derived copy aliased back into the earlier Model: before now %v, want %v", got, beforeVisible)
	}
}

// TestTinyTerminalRendersNoticeNotGarbage is the graceful-degradation render
// guarantee: at a terminal too small to fit even one pane, View renders a
// "terminal too small" notice (clamped to the size) rather than an overlapping
// composite, and a roomier resize recovers the cockpit.
func TestTinyTerminalRendersNoticeNotGarbage(t *testing.T) {
	m, _ := newCockpit(t)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 20, Height: 6})
	// At 20×6 with 4 panes the cockpit can't give every pane the Box minimum, but
	// some panes still fit — the render must be clean (no panic, sized to the term).
	out := m.View()
	if lipgloss.Height(out) > 6 {
		t.Errorf("render overran the terminal height: %d rows for h=6", lipgloss.Height(out))
	}

	// Now shrink below one pane (only 2 rows for panes, below the Box minimum of 3):
	// the notice shows. Width is roomy enough to read it.
	m, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 3})
	out = m.View()
	if !strings.Contains(stripANSI(out), "terminal too small") {
		t.Errorf("tiny terminal should show the too-small notice, got:\n%s", stripANSI(out))
	}

	// A roomy resize recovers the full cockpit.
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if got := m.VisibleIDs(); !contains(got, "presence") || !contains(got, "stream") {
		t.Errorf("cockpit did not recover after resize: %v", got)
	}
}

// TestDetailNeverStepsIntoDroppedPane is the selection-side mirror of the
// small-terminal fix: at a terminal too tight to fit the detail pane, opening
// detail (via the toggle key or a surface OpenMsg) must NOT leave the selection
// stepped into a pane that has no rect. The reflow guard must demote the
// selection to a laid-out pane and drop to the layout level.
//
// It asserts against the ACTUAL laid-out set (Model.LaidOut → m.rects), not the
// View text: View paints the selected pane id into the status bar, so a "detail"
// substring in View is present precisely in the buggy stuck state — reading View
// would make the test vacuous. The test first confirms the regression conditions
// (detail was requested but dropped), so it can never pass for the wrong reason,
// then asserts the demoted state. With update.go reverted to the pre-fix
// ordering (selection set after reflow), the demotion does not happen and this
// test fails.
func TestDetailNeverStepsIntoDroppedPane(t *testing.T) {
	// A tight terminal: the cockpit's right column can't also fit a detail slot at
	// the Box minimum, so arrange drops the detail pane.
	const tw, th = 30, 8

	assertDemoted := func(t *testing.T, m layout.Model, via string) {
		t.Helper()
		// Regression precondition: detail was requested (shown) but dropped (no rect).
		// If this does not hold, the terminal fit detail and the test would not
		// exercise the bug — fail loudly rather than pass vacuously.
		if !m.DetailShown() {
			t.Fatalf("%s: precondition failed — detail was not shown", via)
		}
		if m.LaidOut("detail") {
			t.Fatalf("%s: precondition failed — detail got a rect at %dx%d, so the drop path is untested", via, tw, th)
		}
		// The actual guarantee: a dropped detail pane must not hold the selection,
		// and we must not be stepped into it.
		if m.Selected() == "detail" {
			t.Errorf("%s: selection stuck on dropped detail pane (selected=detail)", via)
		}
		if m.SteppedIn() {
			t.Errorf("%s: stepped in (levelPane) while detail was dropped — selected=%q", via, m.Selected())
		}
		// And the selection must be a pane that actually rendered (or empty).
		if sel := m.Selected(); sel != "" && !m.LaidOut(sel) {
			t.Errorf("%s: selection %q is not in the laid-out set", via, sel)
		}
	}

	t.Run("toggle_key", func(t *testing.T) {
		m, _ := newCockpit(t)
		m, _ = m.Update(tea.WindowSizeMsg{Width: tw, Height: th})
		m, _ = m.Update(key("d")) // toggleDetail
		assertDemoted(t, m, "toggleDetail")
	})

	t.Run("open_intent", func(t *testing.T) {
		m, _ := newCockpit(t)
		m, _ = m.Update(tea.WindowSizeMsg{Width: tw, Height: th})
		m, _ = m.Update(surface.OpenMsg{Kind: surface.OpenArtifact, Ref: "design-doc"}) // openDetail
		assertDemoted(t, m, "openDetail")
	})
}

// TestDetailOnDemandToggle is AC#2: the detail pane is hidden by default,
// toggles in cleanly (visible, selected, stepped-in), and toggles back out
// (hidden, others reclaim the space).
func TestDetailOnDemandToggle(t *testing.T) {
	m, panes := newCockpit(t)
	streamBefore := panes["stream"].w * panes["stream"].h

	m, _ = m.Update(key("d")) // toggle detail in
	if !m.DetailShown() {
		t.Fatal("d should show the detail pane")
	}
	if m.Selected() != "detail" || !m.SteppedIn() {
		t.Errorf("showing detail should select + step in: selected=%q steppedIn=%v", m.Selected(), m.SteppedIn())
	}
	if !contains(m.VisibleIDs(), "detail") {
		t.Errorf("detail not in visible set: %v", m.VisibleIDs())
	}

	// Hide detail again from the options menu (the operator is stepped into detail,
	// so `o` reaches the layout only after stepping out — open the menu after Esc
	// closes it, then confirm the menu toggle path also closes a re-shown detail).
	// First close via the menu while NOT stepped in: re-show, step out, toggle off.
	m, _ = m.Update(key("esc")) // Esc closes detail (per the contract)
	if m.DetailShown() {
		t.Fatal("Esc should close detail")
	}
	if contains(m.VisibleIDs(), "detail") {
		t.Errorf("detail still visible after close: %v", m.VisibleIDs())
	}
	streamAfter := panes["stream"].w * panes["stream"].h
	if streamAfter != streamBefore {
		t.Errorf("after hiding detail, others should reclaim original space: stream %d != %d", streamAfter, streamBefore)
	}
}

// TestEscClosesDetail: stepping out of the detail pane with Esc closes it (detail
// is shown on demand, dismissed when the operator leaves — never a resting
// column). This is the layout-level step-out converging with the surface's own
// DoneMsg close path.
func TestEscClosesDetail(t *testing.T) {
	m, _ := newCockpit(t)
	m, _ = m.Update(key("d")) // show detail, selected + stepped in
	if m.Selected() != "detail" || !m.SteppedIn() {
		t.Fatal("precondition: stepped into detail")
	}
	m, _ = m.Update(key("esc"))
	if m.DetailShown() {
		t.Error("Esc on the detail pane should close it")
	}
	if m.SteppedIn() {
		t.Error("Esc on the detail pane should return to the layout level")
	}
}

// TestOpenMsgShowsDetailAndNotifies is the detail-on-demand intent contract: a
// surface's OpenMsg shows the detail pane AND emits a DetailOpenedMsg (a distinct
// host-facing type, not the raw OpenMsg) so the host can retarget the detail
// content. The layout records the target but never resolves it (stays
// domain-free). Using a distinct type is what keeps a forward-everything host
// from looping.
func TestOpenMsgShowsDetailAndNotifies(t *testing.T) {
	m, _ := newCockpit(t)
	open := surface.OpenMsg{Kind: surface.OpenArtifact, Ref: "design-doc"}
	m2, cmd := m.Update(open)
	if !m2.DetailShown() {
		t.Error("OpenMsg should show the detail pane")
	}
	if m2.Selected() != "detail" || !m2.SteppedIn() {
		t.Errorf("OpenMsg should select + step into detail: selected=%q steppedIn=%v", m2.Selected(), m2.SteppedIn())
	}
	if cmd == nil {
		t.Fatal("OpenMsg must produce a DetailOpenedMsg Cmd for the host to retarget")
	}
	got, ok := cmd().(layout.DetailOpenedMsg)
	if !ok {
		t.Fatalf("notification = %#v, want layout.DetailOpenedMsg", cmd())
	}
	if got.Kind != open.Kind || got.Ref != open.Ref {
		t.Errorf("DetailOpenedMsg = %#v, want Kind/Ref from %#v", got, open)
	}
	if cfg := m2.Config(); cfg.DetailTarget != "design-doc" {
		t.Errorf("detail target not recorded in config: %q", cfg.DetailTarget)
	}
}

// TestDetailOpenedMsgIsInertInLayout proves the loop-proofing: feeding a
// DetailOpenedMsg back into the layout (as a forward-everything host would) does
// NOT re-trigger the open — it produces no further DetailOpenedMsg, so there is
// no infinite re-emit loop.
func TestDetailOpenedMsgIsInertInLayout(t *testing.T) {
	m, _ := newCockpit(t)
	_, cmd := m.Update(layout.DetailOpenedMsg{Kind: surface.OpenArtifact, Ref: "design-doc"})
	if cmd != nil {
		if _, ok := cmd().(layout.DetailOpenedMsg); ok {
			t.Fatal("DetailOpenedMsg fed back into the layout re-triggered another — loop risk")
		}
	}
}

// TestDetailDoneHidesIt: a DoneMsg from the detail pane closes detail-on-demand
// cleanly (hidden + back at the layout level).
func TestDetailDoneHidesIt(t *testing.T) {
	m, _ := newCockpit(t)
	m, _ = m.Update(key("d")) // show detail, stepped in
	m, _ = m.Update(surface.DoneMsg{ID: "detail"})
	if m.DetailShown() {
		t.Error("detail's DoneMsg should hide it")
	}
	if m.SteppedIn() {
		t.Error("detail's DoneMsg should return to the layout level")
	}
}

// TestPresetCycleReflows: the preset key cycles to the next preset and reflows.
func TestPresetCycleReflows(t *testing.T) {
	m, _ := newCockpit(t)
	// Cockpit and split arrange presence differently; assert the cycle changed the
	// active preset via the persisted config.
	start := m.Config().Preset
	m, _ = m.Update(key("p"))
	if m.Config().Preset == start {
		t.Errorf("preset key did not change the preset (still %q)", start)
	}
}

// TestLayoutShortcutsAreOverridable proves the layout reads the detail-toggle and
// preset-cycle keys from the keymap (keys are data, nothing hardcoded): a remapped
// keymap drives them by the new keys, and the default d/p no longer act.
func TestLayoutShortcutsAreOverridable(t *testing.T) {
	keys := theme.DefaultKeymap().Merge(
		theme.Override{Action: "DetailToggle", Keys: []string{"f1"}},
		theme.Override{Action: "PresetCycle", Keys: []string{"f2"}},
	)
	m := layout.New(theme.Dark(), keys, layout.DefaultConfig(),
		newMock("presence", "presence"), newMock("stream", "stream"),
		newMock("artifact", "artifact"), newMock("detail", "detail"))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// The old default keys are inert now.
	m, _ = m.Update(key("d"))
	if m.DetailShown() {
		t.Error("d should be inert after remapping DetailToggle")
	}
	startPreset := m.Config().Preset
	m, _ = m.Update(key("p"))
	if m.Config().Preset != startPreset {
		t.Error("p should be inert after remapping PresetCycle")
	}

	// The remapped keys act.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF1})
	if !m.DetailShown() {
		t.Error("remapped DetailToggle (f1) should toggle the detail pane")
	}
	// Step back to the layout level so f2 is read as a layout key.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF2})
	if m.Config().Preset == startPreset {
		t.Error("remapped PresetCycle (f2) should cycle the preset")
	}
}

// TestConfigApplyOnNew: a Model built from a non-default Config honours the
// preset, hidden set, and theme.
func TestConfigApplyOnNew(t *testing.T) {
	cfg := layout.Config{
		Version: layout.ConfigVersion,
		Preset:  layout.PresetSplit,
		Hidden:  []string{"artifact"},
		Theme:   theme.VariantLight,
	}
	m := layout.New(theme.Dark(), theme.DefaultKeymap(), cfg,
		newMock("presence", "presence"), newMock("stream", "stream"), newMock("artifact", "artifact"))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if m.Config().Preset != layout.PresetSplit {
		t.Errorf("preset not applied: %q", m.Config().Preset)
	}
	if contains(m.VisibleIDs(), "artifact") {
		t.Errorf("hidden artifact should not be visible: %v", m.VisibleIDs())
	}
	if m.Theme().Variant != theme.VariantLight {
		t.Errorf("theme not applied: %q", m.Theme().Variant)
	}
}

// TestQuitTearsDownSurfaces: the Quit binding stops every surface (the Surface
// contract's teardown) and returns tea.Quit.
func TestQuitTearsDownSurfaces(t *testing.T) {
	m, panes := newCockpit(t)
	_, cmd := m.Update(key("q"))
	if cmd == nil {
		t.Fatal("q should return a quit cmd")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("q cmd = %#v, want tea.QuitMsg", cmd())
	}
	for id, p := range panes {
		if p.stopped == 0 {
			t.Errorf("surface %q was not stopped on quit", id)
		}
	}
}

// TestOptionsMenuTogglesTheme: switching the theme from the options menu changes
// the persisted theme variant. The menu stays open so the operator can flip more.
func TestOptionsMenuTogglesTheme(t *testing.T) {
	m, _ := newCockpit(t)
	if m.Theme().Variant != theme.VariantDark {
		t.Fatalf("precondition: dark theme, got %q", m.Theme().Variant)
	}
	m, _ = m.Update(key("o"))
	// Move the cursor to the theme row (it is second-to-last: ...preset, theme,
	// quit). Walk up two from the bottom by going up from the wrap.
	for range 2 { // up twice from row 0 wraps to quit then theme
		m, _ = m.Update(key("up"))
	}
	m, _ = m.Update(key("enter"))
	if m.Theme().Variant != theme.VariantLight {
		t.Errorf("theme not switched via menu: %q", m.Theme().Variant)
	}
	if m.Config().Theme != theme.VariantLight {
		t.Errorf("theme switch not persisted in config: %q", m.Config().Theme)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
