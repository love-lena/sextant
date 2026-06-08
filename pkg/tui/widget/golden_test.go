package widget_test

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
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

// fixedDark pins a dark theme so colour ANSI is stable across machines (Auto
// would vary with the terminal).
func fixedDark() theme.Theme { return theme.Dark() }

func sampleItems(t theme.Theme) []widget.ListItem {
	return []widget.ListItem{
		{Title: "lena", Glyph: glyph(t, theme.StatusConnected), Hue: t.RoleHue(theme.RoleHuman)},
		{Title: "coordinator-1", Glyph: glyph(t, theme.StatusConnected), Hue: t.RoleHue(theme.RoleCoordinator)},
		{Title: "dispatcher-1", Glyph: glyph(t, theme.StatusIdle), Hue: t.RoleHue(theme.RoleDispatcher)},
		{Title: "agent-alpha", Glyph: glyph(t, theme.StatusConnected), Hue: t.RoleHue(theme.RoleAgent)},
		{Title: "agent-beta", Glyph: glyph(t, theme.StatusDraining), Hue: t.RoleHue(theme.RoleAgent)},
		{Title: "bus", Glyph: glyph(t, theme.StatusConnected), Hue: t.RoleHue(theme.RoleSystem)},
	}
}

func glyph(t theme.Theme, s theme.Status) string {
	return lipgloss.NewStyle().Foreground(t.StatusHue(s)).Render(theme.StatusGlyph(s))
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
		{"idle", widget.FocusIdle, 24, 6},
		{"selected", widget.FocusSelected, 24, 6},
		{"active", widget.FocusActive, 24, 6},
		{"narrow", widget.FocusActive, 12, 6},
		{"overflow", widget.FocusActive, 24, 3}, // fewer rows than items
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := widget.NewList(theme.DefaultKeymap(), sampleItems(th)...)
			l.SetSize(tc.w, tc.h)
			out := boxed(th, tc.focus, "presence", th.RoleHue(theme.RoleHuman), l.View(th, tc.focus), tc.w, tc.h)
			teatest.RequireEqualOutput(t, []byte(out))
		})
	}
}

func TestListEmptyGolden(t *testing.T) {
	th := fixedDark()
	l := widget.NewList(theme.DefaultKeymap())
	l.SetSize(24, 6)
	out := boxed(th, widget.FocusSelected, "presence", th.RoleHue(theme.RoleHuman), l.View(th, widget.FocusSelected), 24, 6)
	teatest.RequireEqualOutput(t, []byte(out))
}

func TestListCursorMoveGolden(t *testing.T) {
	th := fixedDark()
	l := widget.NewList(theme.DefaultKeymap(), sampleItems(th)...)
	l.SetSize(24, 6)
	l.MoveDown()
	l.MoveDown()
	out := boxed(th, widget.FocusActive, "presence", th.RoleHue(theme.RoleHuman), l.View(th, widget.FocusActive), 24, 6)
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
		{"idle", widget.FocusIdle, 50, 6},
		{"selected", widget.FocusSelected, 50, 6},
		{"active_tail", widget.FocusActive, 50, 6}, // overflow: ↓ cue absent at tail, ↑ present
		{"narrow", widget.FocusActive, 24, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := widget.NewStream(theme.DefaultKeymap())
			s.SetSize(tc.w, tc.h)
			s.SetLines(sampleStream())
			out := boxed(th, tc.focus, "stream", th.KindHue(theme.KindWorkflowEvent), s.View(th, tc.focus), tc.w, tc.h)
			teatest.RequireEqualOutput(t, []byte(out))
		})
	}
}

func TestStreamScrolledGolden(t *testing.T) {
	th := fixedDark()
	s := widget.NewStream(theme.DefaultKeymap())
	s.SetSize(50, 6)
	s.SetLines(sampleStream())
	// Scroll up off the tail: both ↑ and ↓ overflow cues should show.
	s.ScrollUp()
	s.ScrollUp()
	out := boxed(th, widget.FocusActive, "stream", th.KindHue(theme.KindWorkflowEvent), s.View(th, widget.FocusActive), 50, 6)
	teatest.RequireEqualOutput(t, []byte(out))
}

func TestStreamEmptyGolden(t *testing.T) {
	th := fixedDark()
	s := widget.NewStream(theme.DefaultKeymap())
	s.SetSize(50, 6)
	out := boxed(th, widget.FocusSelected, "stream", th.KindHue(theme.KindWorkflowEvent), s.View(th, widget.FocusSelected), 50, 6)
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
		{"idle", widget.FocusIdle, 36, 8},
		{"selected", widget.FocusSelected, 36, 8},
		{"active", widget.FocusActive, 36, 8},
		{"narrow_reflow", widget.FocusActive, 20, 8}, // narrower → more wrapped lines → overflow
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := widget.NewDetail(theme.DefaultKeymap())
			d.SetSize(tc.w, tc.h)
			d.SetText(sampleDetail)
			out := boxed(th, tc.focus, "detail", th.KindHue(theme.KindArtifactUpdate), d.View(th, tc.focus), tc.w, tc.h)
			teatest.RequireEqualOutput(t, []byte(out))
		})
	}
}

func TestDetailScrolledGolden(t *testing.T) {
	th := fixedDark()
	d := widget.NewDetail(theme.DefaultKeymap())
	d.SetSize(20, 6)
	d.SetText(sampleDetail)
	d.ScrollDown()
	d.ScrollDown()
	out := boxed(th, widget.FocusActive, "detail", th.KindHue(theme.KindArtifactUpdate), d.View(th, widget.FocusActive), 20, 6)
	teatest.RequireEqualOutput(t, []byte(out))
}

func TestDetailEmptyGolden(t *testing.T) {
	th := fixedDark()
	d := widget.NewDetail(theme.DefaultKeymap())
	d.SetSize(36, 8)
	out := boxed(th, widget.FocusSelected, "detail", th.KindHue(theme.KindArtifactUpdate), d.View(th, widget.FocusSelected), 36, 8)
	teatest.RequireEqualOutput(t, []byte(out))
}

// boxed wraps a widget view in the standard chrome so the golden captures both
// the body and the focus-driven border colour the dash actually renders.
func boxed(th theme.Theme, f widget.Focus, title string, hue lipgloss.Color, body string, w, h int) string {
	return widget.Box(th, f, title, hue, body, w, h)
}
