package widget_test

import (
	"testing"

	"github.com/charmbracelet/x/exp/teatest"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/theme"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/widget"
)

// The golden tests render each widget's View deterministically — fixed size,
// fixed content, no time or randomness — and assert it against a committed
// golden via teatest.RequireEqualOutput. Regenerate the goldens with:
//
//	go test ./pkg/tui/widget -update
//
// Rendering View directly (rather than driving a full PTY program) keeps the
// goldens free of cursor-positioning ANSI and timing flakiness; the focus,
// reflow, and overflow behaviour is the same code path the dash runs.
//
// Sizes here are OUTER box dimensions. A widget is sized to the box's inner
// content area (Box draws a 2-col border and a 1-col pad each side, and 1 border
// row top and bottom), exactly as the dash's layout engine does — so Box never
// re-wraps a line the widget already laid out. Use innerOf to convert.

// Box overhead: 2 border columns + 2 padding columns wide, 2 border rows tall.
const (
	boxOverheadW = 4
	boxOverheadH = 2
)

// innerOf returns the inner content area for a box of outer size w×h: the area a
// widget must be sized to so Box wraps exactly its frame around the rendered
// body. Mirrors the gallery's layout().
func innerOf(w, h int) (int, int) { return w - boxOverheadW, h - boxOverheadH }

// fixedDark pins a dark theme so colour ANSI is stable across machines (Auto
// would vary with the terminal).
func fixedDark() theme.Theme { return theme.Dark() }

func sampleItems(t theme.Theme) []widget.ListItem {
	item := func(name, role string, st theme.Status) widget.ListItem {
		return widget.ListItem{
			Title:    name,
			Glyph:    theme.StatusGlyph(st),
			Hue:      t.RoleHue(role),
			GlyphHue: t.StatusHue(st),
		}
	}
	return []widget.ListItem{
		item("lena", theme.RoleHuman, theme.StatusConnected),
		item("coordinator-1", theme.RoleCoordinator, theme.StatusConnected),
		item("dispatcher-1", theme.RoleDispatcher, theme.StatusIdle),
		item("agent-alpha", theme.RoleAgent, theme.StatusConnected),
		item("agent-beta", theme.RoleAgent, theme.StatusDraining),
		item("bus", theme.RoleSystem, theme.StatusConnected),
	}
}

func sampleStream() []string {
	return []string{
		"lena            chat            let's get the dash building",
		"coordinator-1   spawn.request   agent-alpha: theme toolkit",
		"agent-alpha     spawn.ack       accepted, starting",
		"agent-alpha     workflow.event  step 1/3 palette resolved",
		"agent-alpha     workflow.event  step 2/3 widgets compiling",
		"agent-alpha     artifact.update theme.go +210",
		"agent-beta      drain           going offline for redeploy",
		"coordinator-1   chat            nice — eyeball the gallery",
	}
}

const sampleDetail = "Selected: agent-alpha\n\nrole: agent\nstatus: connected\n\n" +
	"This detail pane word-wraps a block of text to its width and scrolls when " +
	"the content runs past the visible height. The overflow cues mark more above " +
	"or below the fold."

// --- cursor list ---

func TestListGolden(t *testing.T) {
	th := fixedDark()
	for _, tc := range []struct {
		name  string
		focus widget.Focus
		w, h  int
	}{
		{"idle", widget.FocusIdle, 28, 8},
		{"selected", widget.FocusSelected, 28, 8},
		{"active", widget.FocusActive, 28, 8},
		{"narrow", widget.FocusActive, 16, 8},   // long titles truncate, never wrap
		{"overflow", widget.FocusActive, 28, 5}, // fewer rows than items
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := widget.NewList(theme.DefaultKeymap(), sampleItems(th)...)
			iw, ih := innerOf(tc.w, tc.h)
			l.SetSize(iw, ih)
			out := widget.Box(th, tc.focus, "presence", th.RoleHue(theme.RoleHuman), l.View(th, tc.focus), tc.w, tc.h)
			teatest.RequireEqualOutput(t, []byte(out))
		})
	}
}

func TestListEmptyGolden(t *testing.T) {
	th := fixedDark()
	l := widget.NewList(theme.DefaultKeymap())
	iw, ih := innerOf(28, 8)
	l.SetSize(iw, ih)
	out := widget.Box(th, widget.FocusSelected, "presence", th.RoleHue(theme.RoleHuman), l.View(th, widget.FocusSelected), 28, 8)
	teatest.RequireEqualOutput(t, []byte(out))
}

func TestListCursorMoveGolden(t *testing.T) {
	th := fixedDark()
	l := widget.NewList(theme.DefaultKeymap(), sampleItems(th)...)
	iw, ih := innerOf(28, 8)
	l.SetSize(iw, ih)
	l.SetCursor(2)
	out := widget.Box(th, widget.FocusActive, "presence", th.RoleHue(theme.RoleHuman), l.View(th, widget.FocusActive), 28, 8)
	teatest.RequireEqualOutput(t, []byte(out))
}

// --- stream viewport ---

func TestStreamGolden(t *testing.T) {
	th := fixedDark()
	for _, tc := range []struct {
		name  string
		focus widget.Focus
		w, h  int
	}{
		{"idle", widget.FocusIdle, 54, 8},
		{"selected", widget.FocusSelected, 54, 8},
		{"active_tail", widget.FocusActive, 54, 8}, // tail: ↑ cue present, ↓ absent
		{"narrow", widget.FocusActive, 28, 8},      // long lines truncate
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := widget.NewStream(theme.DefaultKeymap())
			iw, ih := innerOf(tc.w, tc.h)
			s.SetSize(iw, ih)
			s.SetLines(sampleStream())
			out := widget.Box(th, tc.focus, "stream", th.KindHue(theme.KindWorkflowEvent), s.View(th, tc.focus), tc.w, tc.h)
			teatest.RequireEqualOutput(t, []byte(out))
		})
	}
}

func TestStreamScrolledGolden(t *testing.T) {
	th := fixedDark()
	s := widget.NewStream(theme.DefaultKeymap())
	iw, ih := innerOf(54, 8)
	s.SetSize(iw, ih)
	s.SetLines(sampleStream())
	// Scroll up off the tail: both ↑ and ↓ overflow cues should show.
	s.ScrollUp()
	s.ScrollUp()
	out := widget.Box(th, widget.FocusActive, "stream", th.KindHue(theme.KindWorkflowEvent), s.View(th, widget.FocusActive), 54, 8)
	teatest.RequireEqualOutput(t, []byte(out))
}

func TestStreamEmptyGolden(t *testing.T) {
	th := fixedDark()
	s := widget.NewStream(theme.DefaultKeymap())
	iw, ih := innerOf(54, 8)
	s.SetSize(iw, ih)
	out := widget.Box(th, widget.FocusSelected, "stream", th.KindHue(theme.KindWorkflowEvent), s.View(th, widget.FocusSelected), 54, 8)
	teatest.RequireEqualOutput(t, []byte(out))
}

// --- detail pane ---

func TestDetailGolden(t *testing.T) {
	th := fixedDark()
	for _, tc := range []struct {
		name  string
		focus widget.Focus
		w, h  int
	}{
		{"idle", widget.FocusIdle, 40, 10},
		{"selected", widget.FocusSelected, 40, 10},
		{"active", widget.FocusActive, 40, 10},
		{"narrow_reflow", widget.FocusActive, 24, 8}, // narrower → more wrapped lines → overflow
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := widget.NewDetail(theme.DefaultKeymap())
			iw, ih := innerOf(tc.w, tc.h)
			d.SetSize(iw, ih)
			d.SetText(sampleDetail)
			out := widget.Box(th, tc.focus, "detail", th.KindHue(theme.KindArtifactUpdate), d.View(th, tc.focus), tc.w, tc.h)
			teatest.RequireEqualOutput(t, []byte(out))
		})
	}
}

func TestDetailScrolledGolden(t *testing.T) {
	th := fixedDark()
	d := widget.NewDetail(theme.DefaultKeymap())
	iw, ih := innerOf(24, 8)
	d.SetSize(iw, ih)
	d.SetText(sampleDetail)
	d.ScrollDown()
	d.ScrollDown()
	out := widget.Box(th, widget.FocusActive, "detail", th.KindHue(theme.KindArtifactUpdate), d.View(th, widget.FocusActive), 24, 8)
	teatest.RequireEqualOutput(t, []byte(out))
}

func TestDetailEmptyGolden(t *testing.T) {
	th := fixedDark()
	d := widget.NewDetail(theme.DefaultKeymap())
	iw, ih := innerOf(40, 10)
	d.SetSize(iw, ih)
	out := widget.Box(th, widget.FocusSelected, "detail", th.KindHue(theme.KindArtifactUpdate), d.View(th, widget.FocusSelected), 40, 10)
	teatest.RequireEqualOutput(t, []byte(out))
}
