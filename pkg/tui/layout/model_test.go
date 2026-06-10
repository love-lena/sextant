package layout_test

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/love-lena/sextant/pkg/tui/layout"
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
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "ctrl+h":
		return tea.KeyMsg{Type: tea.KeyCtrlH}
	case "ctrl+j":
		return tea.KeyMsg{Type: tea.KeyCtrlJ}
	case "ctrl+k":
		return tea.KeyMsg{Type: tea.KeyCtrlK}
	case "ctrl+l":
		return tea.KeyMsg{Type: tea.KeyCtrlL}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
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

// TestStartsFocused: a fresh cockpit focuses the first visible pane (ADR-0026:
// one pane is always focused) — it is active, the other visible panes are
// selected (the muted-cursor unfocused state), never idle.
func TestStartsFocused(t *testing.T) {
	m, panes := newCockpit(t)
	if m.Focused() != "clients" {
		t.Errorf("first focus = %q, want clients", m.Focused())
	}
	if m.FocusOf("clients") != widget.FocusActive {
		t.Errorf("focused pane = %v, want active", m.FocusOf("clients"))
	}
	for _, id := range []string{"topics", "artifacts"} {
		if m.FocusOf(id) != widget.FocusSelected {
			t.Errorf("unfocused visible pane %q = %v, want selected", id, m.FocusOf(id))
		}
		if panes[id].focus != widget.FocusSelected {
			t.Errorf("surface %q was not told it is selected: %v", id, panes[id].focus)
		}
	}
	if got := m.VisibleIDs(); !reflect.DeepEqual(got, []string{"clients", "topics", "artifacts"}) {
		t.Errorf("visible = %v, want clients/topics/artifacts", got)
	}
}

// TestCycleFocusWraps: Tab cycles focus forward through the visible panes and
// wraps; Shift+Tab cycles back and wraps the other way.
func TestCycleFocusWraps(t *testing.T) {
	m, _ := newCockpit(t)
	for _, want := range []string{"topics", "artifacts", "clients"} {
		m, _ = m.Update(key("tab"))
		if m.Focused() != want {
			t.Fatalf("tab: focus = %q, want %q", m.Focused(), want)
		}
	}
	// Shift+Tab from clients wraps backward to artifacts.
	m, _ = m.Update(key("shift+tab"))
	if m.Focused() != "artifacts" {
		t.Fatalf("shift+tab from clients = %q, want artifacts (wrap)", m.Focused())
	}
}

// TestSpatialFocus: Ctrl+h/j/k/l move focus by geometry, not a flat
// forward/back order. The cockpit lays the three browsers as full-height
// columns side by side, so left/right walk across them (no wrap at the ends)
// and up/down hold (nothing above or below a full-height column).
func TestSpatialFocus(t *testing.T) {
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

	// Ctrl+l walks right across the columns: clients → topics → artifacts, then
	// holds (no wrap — the cycle keys wrap instead).
	m, _ = m.Update(key("ctrl+l"))
	if m.Focused() != "topics" {
		t.Fatalf("ctrl+l from clients = %q, want topics", m.Focused())
	}
	m, _ = m.Update(key("ctrl+l"))
	if m.Focused() != "artifacts" {
		t.Fatalf("ctrl+l from topics = %q, want artifacts", m.Focused())
	}
	m, _ = m.Update(key("ctrl+l"))
	if m.Focused() != "artifacts" {
		t.Fatalf("ctrl+l from artifacts should hold (no wrap), got %q", m.Focused())
	}
	// Ctrl+h walks back.
	m, _ = m.Update(key("ctrl+h"))
	if m.Focused() != "topics" {
		t.Fatalf("ctrl+h from artifacts = %q, want topics", m.Focused())
	}
	m, _ = m.Update(key("ctrl+h"))
	if m.Focused() != "clients" {
		t.Fatalf("ctrl+h from topics = %q, want clients", m.Focused())
	}
	// Ctrl+j/k hold: nothing above or below a full-height column.
	m, _ = m.Update(key("ctrl+j"))
	if m.Focused() != "clients" {
		t.Fatalf("ctrl+j in a single-row cockpit should hold, got %q", m.Focused())
	}
	m, _ = m.Update(key("ctrl+k"))
	if m.Focused() != "clients" {
		t.Fatalf("ctrl+k in a single-row cockpit should hold, got %q", m.Focused())
	}
}

// TestSpatialFocusSplitPreset covers a preset whose geometry differs from the
// cockpit: the split grid lays clients top-left, topics top-right, and artifacts
// across the full-width bottom row. Ctrl+j from a top pane reaches the bottom
// row; Ctrl+k from the bottom returns to the top.
func TestSpatialFocusSplitPreset(t *testing.T) {
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
	if m.Focused() != "clients" {
		t.Fatalf("precondition: clients should start focused, got %q", m.Focused())
	}

	// Ctrl+l from clients → topics (top-right).
	m, _ = m.Update(key("ctrl+l"))
	if m.Focused() != "topics" {
		t.Fatalf("ctrl+l from clients (split) = %q, want topics", m.Focused())
	}
	// Ctrl+j from topics → artifacts (the full-width bottom row).
	m, _ = m.Update(key("ctrl+j"))
	if m.Focused() != "artifacts" {
		t.Fatalf("ctrl+j from topics (split) = %q, want artifacts", m.Focused())
	}
	// Ctrl+k from artifacts → clients (reading-order first of the top row it spans).
	m, _ = m.Update(key("ctrl+k"))
	if m.Focused() != "clients" {
		t.Fatalf("ctrl+k from artifacts (split) = %q, want clients", m.Focused())
	}
}

// TestFocusMoveLeavesContentUntouched is ADR-0026's core guarantee: a focus
// move is never delivered to any surface, so it cannot change what a pane
// shows. The mock records every key it is delivered; after a tour of focus
// moves, every pane's record is empty.
func TestFocusMoveLeavesContentUntouched(t *testing.T) {
	m, panes := newCockpit(t)
	for _, k := range []string{"tab", "ctrl+l", "ctrl+h", "shift+tab", "ctrl+j", "ctrl+k"} {
		m, _ = m.Update(key(k))
	}
	for id, p := range panes {
		if len(p.keys) != 0 {
			t.Errorf("focus moves were delivered to surface %q: %v", id, p.keys)
		}
	}
}

// TestContentKeysGoToFocusedPane: a non-focus key (arrows, Enter, Esc, runes)
// is delivered to the focused surface and only to it — there is no layout
// level claiming them (ADR-0026).
func TestContentKeysGoToFocusedPane(t *testing.T) {
	m, panes := newCockpit(t)
	m, _ = m.Update(key("tab")) // focus topics
	for _, k := range []string{"up", "down", "left", "right", "enter", "esc", "x"} {
		m, _ = m.Update(key(k))
	}
	want := []string{"up", "down", "left", "right", "enter", "esc", "x"}
	if !reflect.DeepEqual(panes["topics"].keys, want) {
		t.Errorf("focused pane keys = %v, want %v", panes["topics"].keys, want)
	}
	for _, id := range []string{"clients", "artifacts"} {
		if len(panes[id].keys) != 0 {
			t.Errorf("unfocused pane %q received keys: %v", id, panes[id].keys)
		}
	}
}

// TestQuitVsCapturingText pins the ADR-0026 quit rule: q quits while the
// focused surface is not capturing text; while it is capturing, q (and the
// other printable chrome keys) are delivered to the surface; ctrl+c quits
// either way.
func TestQuitVsCapturingText(t *testing.T) {
	t.Run("q_quits_at_a_list", func(t *testing.T) {
		m, panes := newCockpit(t)
		_, cmd := m.Update(key("q"))
		if cmd == nil {
			t.Fatal("q should return a quit cmd when the focused surface is not capturing")
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("q cmd = %#v, want tea.QuitMsg", cmd())
		}
		for id, p := range panes {
			if p.stopped == 0 {
				t.Errorf("surface %q was not stopped on quit", id)
			}
		}
	})

	t.Run("q_types_while_composing", func(t *testing.T) {
		m, panes := newCockpit(t)
		panes["clients"].capturing = true // the focused pane holds a live compose
		m, cmd := m.Update(key("q"))
		if cmd != nil {
			t.Fatalf("q while capturing must not quit; got cmd %#v", cmd())
		}
		if !reflect.DeepEqual(panes["clients"].keys, []string{"q"}) {
			t.Errorf("q was not delivered to the capturing surface: %v", panes["clients"].keys)
		}
		// The other printable chrome keys type too — p and o reach the compose.
		m, _ = m.Update(key("p"))
		m, _ = m.Update(key("o"))
		if got := panes["clients"].keys; !reflect.DeepEqual(got, []string{"q", "p", "o"}) {
			t.Errorf("chrome keys not delivered while capturing: %v", got)
		}
		if m.Config().Preset != layout.PresetCockpit {
			t.Error("p while capturing must not cycle the preset")
		}
	})

	t.Run("ctrl_c_always_quits", func(t *testing.T) {
		m, panes := newCockpit(t)
		panes["clients"].capturing = true
		_, cmd := m.Update(key("ctrl+c"))
		if cmd == nil {
			t.Fatal("ctrl+c should quit even while capturing")
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("ctrl+c cmd = %#v, want tea.QuitMsg", cmd())
		}
	})

	t.Run("focus_keys_work_while_composing", func(t *testing.T) {
		m, panes := newCockpit(t)
		panes["clients"].capturing = true
		m, _ = m.Update(key("ctrl+l"))
		if m.Focused() != "topics" {
			t.Errorf("ctrl+l while capturing = %q, want topics (focus keys are never claimed by a surface)", m.Focused())
		}
		if len(panes["clients"].keys) != 0 {
			t.Errorf("the focus key leaked into the capturing surface: %v", panes["clients"].keys)
		}
	})
}

// TestPastedTextNeverMatchesBindings pins the burst/paste guard: an unbracketed
// paste (or fast input burst) arrives as ONE multi-rune KeyRunes message whose
// String() is the literal text — which can spell a binding name. Pasting the
// text "ctrl+c" must not quit, "tab" must not move focus, "q lands" must not
// quit; every chunk is content, delivered to the focused surface. (Found live:
// a tmux send-keys burst containing the words "ctrl+c" force-quit the dash.)
func TestPastedTextNeverMatchesBindings(t *testing.T) {
	m, panes := newCockpit(t)
	chunk := func(s string) tea.KeyMsg {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	var cmd tea.Cmd
	for _, text := range []string{"ctrl+c", "tab", "shift+tab", "ctrl+l", "q lands", "esc", "p"} {
		m, cmd = m.Update(chunk(text))
		if cmd != nil {
			t.Fatalf("pasted %q produced a command (quit?): %#v", text, cmd())
		}
	}
	if m.Focused() != "clients" {
		t.Errorf("pasted text moved focus to %q", m.Focused())
	}
	// Every chunk was delivered to the focused surface as content — except the
	// single-rune "p", which IS the preset key when not capturing (a one-rune
	// chunk is indistinguishable from the keystroke, correctly).
	want := []string{"ctrl+c", "tab", "shift+tab", "ctrl+l", "q lands", "esc"}
	if !reflect.DeepEqual(panes["clients"].keys, want) {
		t.Errorf("focused pane received %v, want %v", panes["clients"].keys, want)
	}
}

// TestMenuPastedTextNeverMatchesBindings pins the same guard BEHIND the options
// menu: the menu matches bindings too, so the chunk guard must run before menu
// dispatch. Pasting the text "ctrl+c" over an open menu must not quit, "enter"
// must not activate the highlighted row, "esc" must not close the menu; with no
// text consumer under the menu the chunk is dropped, never delivered behind it.
// (The R5 review proved the unguarded menu path quit the dash live.)
func TestMenuPastedTextNeverMatchesBindings(t *testing.T) {
	m, panes := newCockpit(t)
	m, _ = m.Update(key("o")) // open the options menu
	if !m.MenuOpen() {
		t.Fatal("precondition: options menu should be open")
	}
	visibleBefore := m.VisibleIDs()
	chunk := func(s string) tea.KeyMsg {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	var cmd tea.Cmd
	for _, text := range []string{"ctrl+c", "enter", "esc", "up", "tab"} {
		m, cmd = m.Update(chunk(text))
		if cmd != nil {
			t.Fatalf("pasted %q over the menu produced a command (quit?): %#v", text, cmd())
		}
	}
	if !m.MenuOpen() {
		t.Error("pasted text closed the options menu")
	}
	if !reflect.DeepEqual(m.VisibleIDs(), visibleBefore) {
		t.Errorf("pasted text toggled a pane: visible %v, want %v", m.VisibleIDs(), visibleBefore)
	}
	if got := panes["clients"].keys; len(got) != 0 {
		t.Errorf("chunks leaked behind the menu to the focused surface: %v", got)
	}
}

// TestHideFocusedMovesFocusToNeighbour: toggling the focused pane off moves
// focus to a remaining visible pane (never "", never a hidden pane).
func TestHideFocusedMovesFocusToNeighbour(t *testing.T) {
	m, _ := newCockpit(t)
	if m.Focused() != "clients" {
		t.Fatalf("precondition: clients focused, got %q", m.Focused())
	}
	m, _ = m.Update(key("o"))     // open options; cursor row 0 is "pane clients"
	m, _ = m.Update(key("enter")) // toggle clients off
	m, _ = m.Update(key("esc"))   // close menu
	if contains(m.VisibleIDs(), "clients") {
		t.Fatalf("precondition: clients should be hidden, visible = %v", m.VisibleIDs())
	}
	if m.Focused() != "topics" {
		t.Errorf("hiding the focused pane should move focus to a neighbour, got %q", m.Focused())
	}
	if m.FocusOf("topics") != widget.FocusActive {
		t.Errorf("the new focused pane = %v, want active", m.FocusOf("topics"))
	}
	if m.FocusOf("clients") != widget.FocusIdle {
		t.Errorf("the hidden pane = %v, want idle", m.FocusOf("clients"))
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

// TestLayoutShortcutsAreOverridable proves the layout reads its keys from the
// keymap (keys are data, nothing hardcoded): a remapped keymap drives the
// preset cycle and the focus cycle by the new keys, and the defaults no longer
// act.
func TestLayoutShortcutsAreOverridable(t *testing.T) {
	keys := theme.DefaultKeymap().Merge(
		theme.Override{Action: "PresetCycle", Keys: []string{"f2"}},
		theme.Override{Action: "FocusNext", Keys: []string{"f3"}},
	)
	m := layout.New(theme.Dark(), keys, layout.DefaultConfig(),
		newMock("clients", "Clients"), newMock("topics", "Topics"),
		newMock("artifacts", "Artifacts"))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// The old default keys are inert now (p falls through to the focused surface;
	// tab is delivered as content).
	startPreset := m.Config().Preset
	m, _ = m.Update(key("p"))
	if m.Config().Preset != startPreset {
		t.Error("p should be inert after remapping PresetCycle")
	}
	m, _ = m.Update(key("tab"))
	if m.Focused() != "clients" {
		t.Error("tab should be inert after remapping FocusNext")
	}

	// The remapped keys act.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF2})
	if m.Config().Preset == startPreset {
		t.Error("remapped PresetCycle (f2) should cycle the preset")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyF3})
	if m.Focused() != "topics" {
		t.Error("remapped FocusNext (f3) should cycle the focus")
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

// TestSingleCharPastedKeyIsContent closes the length-guard hole: a bracketed
// paste arrives with KeyMsg.Paste set, and a SINGLE-character paste ("q", "o",
// "p") has len(Runes)==1, so a multi-rune guard alone would let it reach the
// binding matches. A paste is content in every size: it must not quit, open
// the menu, or cycle the preset — it is delivered to the focused surface.
func TestSingleCharPastedKeyIsContent(t *testing.T) {
	m, panes := newCockpit(t)
	paste := func(s string) tea.KeyMsg {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Paste: true}
	}
	var cmd tea.Cmd
	for _, text := range []string{"q", "o", "p"} {
		m, cmd = m.Update(paste(text))
		if cmd != nil {
			t.Fatalf("pasting %q produced a command (quit?): %#v", text, cmd())
		}
	}
	if m.MenuOpen() {
		t.Error("pasting \"o\" opened the options menu")
	}
	if m.Config().Preset != layout.PresetCockpit {
		t.Error("pasting \"p\" cycled the preset")
	}
	for id, p := range panes {
		if p.stopped != 0 {
			t.Errorf("pasting \"q\" stopped surface %q", id)
		}
	}
	// Each paste reached the focused surface as content, nothing else.
	if got := len(panes["clients"].keys); got != 3 {
		t.Errorf("focused pane received %d keys (%v), want the 3 pasted chunks", got, panes["clients"].keys)
	}
	for _, id := range []string{"topics", "artifacts"} {
		if len(panes[id].keys) != 0 {
			t.Errorf("unfocused pane %q received keys: %v", id, panes[id].keys)
		}
	}
}

// TestAllPanesHiddenShowsHonestNotice: hiding every pane is allowed (the
// operator stays free), and the empty cockpit names its real state — "all
// panes hidden", with the way back in — never the false "terminal too small"
// diagnosis. The options menu keeps working over the notice (visible, not
// just live), so the state is recoverable from inside it.
func TestAllPanesHiddenShowsHonestNotice(t *testing.T) {
	m, _ := newCockpit(t)
	// Toggle all three panes off from the options menu (rows 0..2 are the panes).
	m, _ = m.Update(key("o"))
	for range 3 {
		m, _ = m.Update(key("enter"))
		m, _ = m.Update(key("down"))
	}
	if got := m.VisibleIDs(); len(got) != 0 {
		t.Fatalf("precondition: all panes hidden, still visible: %v", got)
	}

	// With the menu still open it stays VISIBLE, composited over the notice
	// (the centred panel covers the centred text — what matters is that the
	// operator still sees the menu they are driving, and no misdiagnosis).
	out := stripANSI(m.View())
	if !strings.Contains(out, "options") {
		t.Errorf("the options menu must stay visible over the empty cockpit, got:\n%s", out)
	}
	if strings.Contains(out, "terminal too small") {
		t.Errorf("all-hidden must not be misdiagnosed as a too-small terminal:\n%s", out)
	}

	// Menu closed: the honest notice, not the too-small misdiagnosis.
	m, _ = m.Update(key("esc"))
	out = stripANSI(m.View())
	if !strings.Contains(out, "all panes hidden") {
		t.Errorf("empty cockpit should say so, got:\n%s", out)
	}
	if strings.Contains(out, "terminal too small") {
		t.Errorf("all-hidden must not be misdiagnosed as a too-small terminal:\n%s", out)
	}

	// The escape hatch: o reopens the menu, and toggling a pane back recovers.
	m, _ = m.Update(key("o"))
	if !m.MenuOpen() {
		t.Fatal("o should reopen the options menu over the empty cockpit")
	}
	m, _ = m.Update(key("enter")) // cursor row 0: toggle clients back on
	if !contains(m.VisibleIDs(), "clients") {
		t.Fatalf("toggling a pane back on should recover, visible: %v", m.VisibleIDs())
	}
	if m.Focused() != "clients" {
		t.Errorf("focus should land on the reopened pane, got %q", m.Focused())
	}
}
