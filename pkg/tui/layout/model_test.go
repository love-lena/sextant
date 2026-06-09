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

// newCockpit builds a Model over the three browser panes (ADR-0024: clients ·
// topics · artifacts, side by side), sized to a fixed terminal, ready for the
// focus/reflow assertions.
func newCockpit(t *testing.T) (layout.Model, map[string]*mockSurface) {
	t.Helper()
	panes := map[string]*mockSurface{
		"clients":   newMock("clients", "Clients"),
		"topics":    newMock("topics", "Topics"),
		"artifacts": newMock("artifacts", "Artifacts"),
	}
	m := layout.New(theme.Dark(), theme.DefaultKeymap(), layout.DefaultConfig(),
		panes["clients"], panes["topics"], panes["artifacts"])
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m, panes
}

// TestStartsAtLayoutLevel: a fresh cockpit selects the first visible pane and is
// at the layout level (not stepped in).
func TestStartsAtLayoutLevel(t *testing.T) {
	m, _ := newCockpit(t)
	if m.Selected() != "clients" {
		t.Errorf("first selection = %q, want clients", m.Selected())
	}
	if m.SteppedIn() {
		t.Error("should start at layout level, not stepped in")
	}
	if got := m.VisibleIDs(); !reflect.DeepEqual(got, []string{"clients", "topics", "artifacts"}) {
		t.Errorf("visible = %v, want clients/topics/artifacts", got)
	}
}

// TestNavMovesSelection: a nav key moves the selection to the spatially nearest
// pane in that direction; the selected pane carries the accent (selected) border,
// the others idle.
func TestNavMovesSelection(t *testing.T) {
	m, _ := newCockpit(t)
	if m.FocusOf("clients") != widget.FocusSelected {
		t.Errorf("clients focus = %v, want selected", m.FocusOf("clients"))
	}
	m, _ = m.Update(key("right"))
	if m.Selected() != "topics" {
		t.Errorf("after right, selected = %q, want topics", m.Selected())
	}
	if m.FocusOf("clients") != widget.FocusIdle {
		t.Errorf("clients should be idle after moving off it, got %v", m.FocusOf("clients"))
	}
	if m.FocusOf("topics") != widget.FocusSelected {
		t.Errorf("topics focus = %v, want selected", m.FocusOf("topics"))
	}
}

// TestSpatialNav: at the layout level the arrows move the selection by geometry,
// not a flat forward/back order. The cockpit lays the three browsers as
// full-height columns side by side, so Left/Right walk across them and Up/Down
// hold (nothing above or below a full-height column).
func TestSpatialNav(t *testing.T) {
	// Confirm the geometry the assertions navigate against, so a future preset
	// change can't silently make the test pass for the wrong reason.
	m, _ := newCockpit(t)
	cx, cy, _, ch, _ := m.RectXYWH("clients")
	tx, ty, _, _, _ := m.RectXYWH("topics")
	ax, ay, _, _, _ := m.RectXYWH("artifacts")
	if !(cy == 0 && ty == 0 && ay == 0 && cx < tx && tx < ax) {
		t.Fatalf("precondition: three side-by-side columns (clients x=%d, topics x=%d, artifacts x=%d)", cx, tx, ax)
	}
	if ch != 29 { // areaH = 30 - 1 hint row
		t.Fatalf("precondition: full-height columns (clients h=%d)", ch)
	}

	sel := func(m layout.Model) string { return m.Selected() }

	// Right walks across the columns: clients → topics → artifacts, then holds.
	m, _ = m.Update(key("right"))
	if sel(m) != "topics" {
		t.Fatalf("right from clients = %q, want topics", sel(m))
	}
	m, _ = m.Update(key("right"))
	if sel(m) != "artifacts" {
		t.Fatalf("right from topics = %q, want artifacts", sel(m))
	}
	m, _ = m.Update(key("right"))
	if sel(m) != "artifacts" {
		t.Fatalf("right from artifacts should hold (no wrap), got %q", sel(m))
	}
	// Left walks back.
	m, _ = m.Update(key("left"))
	if sel(m) != "topics" {
		t.Fatalf("left from artifacts = %q, want topics", sel(m))
	}
	m, _ = m.Update(key("left"))
	if sel(m) != "clients" {
		t.Fatalf("left from topics = %q, want clients", sel(m))
	}
	// Up/Down hold: nothing above or below a full-height column.
	m, _ = m.Update(key("down"))
	if sel(m) != "clients" {
		t.Fatalf("down in a single-row cockpit should hold, got %q", sel(m))
	}
	m, _ = m.Update(key("up"))
	if sel(m) != "clients" {
		t.Fatalf("up in a single-row cockpit should hold, got %q", sel(m))
	}
}

// TestSpatialNavSplitPreset covers a preset whose geometry differs from the
// cockpit: the split grid lays clients top-left, topics top-right, and artifacts
// across the full-width bottom row. Down from a top pane reaches the bottom row;
// Up from the bottom returns to the top.
func TestSpatialNavSplitPreset(t *testing.T) {
	m, _ := newCockpit(t)
	for m.Config().Preset != "split" {
		m, _ = m.Update(key("p"))
	}
	// Geometry precondition: clients top-left, topics top-right, artifacts bottom.
	_, cy, _, _, _ := m.RectXYWH("clients")
	tx, ty, _, _, _ := m.RectXYWH("topics")
	_, ay, _, _, _ := m.RectXYWH("artifacts")
	if !(cy == 0 && ty == 0 && tx > 0 && ay > 0) {
		t.Fatalf("precondition: split should be clients/topics top, artifacts bottom (cy=%d tx=%d ty=%d ay=%d)", cy, tx, ty, ay)
	}
	if m.Selected() != "clients" {
		t.Fatalf("precondition: clients should start selected, got %q", m.Selected())
	}

	// Right from clients → topics (top-right).
	m, _ = m.Update(key("right"))
	if m.Selected() != "topics" {
		t.Fatalf("right from clients (split) = %q, want topics", m.Selected())
	}
	// Down from topics → artifacts (the full-width bottom row).
	m, _ = m.Update(key("down"))
	if m.Selected() != "artifacts" {
		t.Fatalf("down from topics (split) = %q, want artifacts", m.Selected())
	}
	// Up from artifacts → clients (reading-order first of the top row it spans).
	m, _ = m.Update(key("up"))
	if m.Selected() != "clients" {
		t.Fatalf("up from artifacts (split) = %q, want clients", m.Selected())
	}
}

// TestStepInOutWithKeys: Enter steps into the selected pane (it goes active and
// receives input); Esc is DELIVERED to the surface (never acted on by the
// layout — an active surface can be nested, ADR-0024), whose DoneMsg steps back
// out (selected). This is the locked two-level model with the surface-owned
// step-out.
func TestStepInOutWithKeys(t *testing.T) {
	m, panes := newCockpit(t)
	m, _ = m.Update(key("enter"))
	if !m.SteppedIn() {
		t.Fatal("Enter should step in")
	}
	if m.FocusOf("clients") != widget.FocusActive {
		t.Errorf("stepped-in pane focus = %v, want active", m.FocusOf("clients"))
	}
	if panes["clients"].focus != widget.FocusActive {
		t.Errorf("surface was not told it is active: %v", panes["clients"].focus)
	}
	// Esc routes to the surface and produces its DoneMsg; the layout itself has
	// not stepped out yet (the surface owns its levels).
	m, cmd := m.Update(key("esc"))
	if !m.SteppedIn() {
		t.Fatal("the layout must not step out on Esc itself — that is the surface's DoneMsg")
	}
	if cmd == nil {
		t.Fatal("active Esc should route to the surface and produce its DoneMsg cmd")
	}
	done, ok := cmd().(surface.DoneMsg)
	if !ok || done.ID != "clients" {
		t.Fatalf("expected DoneMsg{clients}, got %#v", cmd())
	}
	// Feeding the DoneMsg back (as bubbletea would) lands at the layout level.
	m, _ = m.Update(done)
	if m.SteppedIn() {
		t.Error("the surface's DoneMsg should step out")
	}
	if m.FocusOf("clients") != widget.FocusSelected {
		t.Errorf("stepped-out pane focus = %v, want selected", m.FocusOf("clients"))
	}
}

// TestStepInRoutesKeysToActiveSurface: while stepped in, a key reaches the active
// surface's Update (and not the others).
func TestStepInRoutesKeysToActiveSurface(t *testing.T) {
	m, _ := newCockpit(t)
	m, _ = m.Update(key("right")) // select topics
	m, _ = m.Update(key("enter"))
	if !m.SteppedIn() {
		t.Fatal("should be stepped into topics")
	}
	// Esc while active routes to the surface, which emits DoneMsg; feeding that back
	// steps out.
	_, cmd := m.Update(key("esc"))
	if cmd == nil {
		t.Fatal("active Esc should route to surface and produce its DoneMsg cmd")
	}
	msg := cmd()
	done, ok := msg.(surface.DoneMsg)
	if !ok || done.ID != "topics" {
		t.Fatalf("expected DoneMsg{topics}, got %#v", msg)
	}
}

// TestDoneMsgStepsOut: a surface's own DoneMsg returns focus to the layout level.
func TestDoneMsgStepsOut(t *testing.T) {
	m, _ := newCockpit(t)
	m, _ = m.Update(key("enter"))
	if !m.SteppedIn() {
		t.Fatal("precondition: stepped in")
	}
	m, _ = m.Update(surface.DoneMsg{ID: "clients"})
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
	// Cursor row 0 is "pane clients"; Enter toggles it off.
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("esc")) // close menu

	afterVisible := m.VisibleIDs()
	if contains(afterVisible, "clients") {
		t.Errorf("clients should be hidden, visible = %v", afterVisible)
	}
	afterArea := totalInner(panes, afterVisible)
	// Fewer panes over the same terminal → less box overhead → MORE inner area for
	// the survivors. The freed space is reclaimed, never left as a gap.
	if afterArea <= beforeArea {
		t.Errorf("reflow did not reclaim freed space: remaining inner area %d not > %d", afterArea, beforeArea)
	}

	// Toggle clients back on via the menu.
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
	w1 := panes["clients"].w
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	w2 := panes["clients"].w
	if w2 <= w1 {
		t.Errorf("resize did not re-fit: clients width %d not > %d", w2, w1)
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
	after, _ = after.Update(key("down")) // move to "pane topics"
	after, _ = after.Update(key("enter"))
	after, _ = after.Update(key("esc"))

	if contains(after.VisibleIDs(), "topics") {
		t.Fatalf("precondition: topics should be hidden on the derived copy, got %v", after.VisibleIDs())
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
	m, _ = m.Update(tea.WindowSizeMsg{Width: 10, Height: 6})
	// At 10×6 the cockpit can't give every pane the Box minimum, but some panes
	// still fit — the render must be clean (no panic, sized to the term).
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
	if got := m.VisibleIDs(); !contains(got, "clients") || !contains(got, "topics") {
		t.Errorf("cockpit did not recover after resize: %v", got)
	}
}

// TestPresetCycleReflows: the preset key cycles to the next preset and reflows.
func TestPresetCycleReflows(t *testing.T) {
	m, _ := newCockpit(t)
	// Cockpit and split arrange the panes differently; assert the cycle changed the
	// active preset via the persisted config.
	start := m.Config().Preset
	m, _ = m.Update(key("p"))
	if m.Config().Preset == start {
		t.Errorf("preset key did not change the preset (still %q)", start)
	}
}

// TestLayoutShortcutsAreOverridable proves the layout reads the preset-cycle key
// from the keymap (keys are data, nothing hardcoded): a remapped keymap drives it
// by the new key, and the default p no longer acts.
func TestLayoutShortcutsAreOverridable(t *testing.T) {
	keys := theme.DefaultKeymap().Merge(
		theme.Override{Action: "PresetCycle", Keys: []string{"f2"}},
	)
	m := layout.New(theme.Dark(), keys, layout.DefaultConfig(),
		newMock("clients", "Clients"), newMock("topics", "Topics"),
		newMock("artifacts", "Artifacts"))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// The old default key is inert now.
	startPreset := m.Config().Preset
	m, _ = m.Update(key("p"))
	if m.Config().Preset != startPreset {
		t.Error("p should be inert after remapping PresetCycle")
	}

	// The remapped key acts.
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
		Hidden:  []string{"artifacts"},
		Theme:   theme.VariantLight,
	}
	m := layout.New(theme.Dark(), theme.DefaultKeymap(), cfg,
		newMock("clients", "Clients"), newMock("topics", "Topics"), newMock("artifacts", "Artifacts"))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if m.Config().Preset != layout.PresetSplit {
		t.Errorf("preset not applied: %q", m.Config().Preset)
	}
	if contains(m.VisibleIDs(), "artifacts") {
		t.Errorf("hidden artifacts should not be visible: %v", m.VisibleIDs())
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
// the persisted theme variant AND re-themes every mounted surface (so the pane
// bodies follow, not just the chrome the layout owns). The menu stays open so
// the operator can flip more.
func TestOptionsMenuTogglesTheme(t *testing.T) {
	m, panes := newCockpit(t)
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
	// Every mounted surface saw the new theme through SetTheme — the wiring that
	// makes the toggle re-theme pane bodies, not just the layout chrome.
	for id, p := range panes {
		if p.themed != theme.VariantLight {
			t.Errorf("surface %q not re-themed via SetTheme (themed=%q)", id, p.themed)
		}
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
