package widget_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// ansi strips SGR escape sequences so a test can assert on glyphs and widths
// without colour codes in the way.
var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansi.ReplaceAllString(s, "") }

// --- overflow cues (regression for the "cues never render" defect) ---

func TestStreamTailShowsTopCueOnly(t *testing.T) {
	th := theme.Dark()
	s := widget.NewStream(theme.DefaultKeymap())
	s.SetSize(40, 4) // 8 sample lines into 4 rows: overflow
	s.SetLines(sampleStream())
	// Following the tail: last line visible, more above ⇒ ↑ cue, no ↓ cue.
	out := plain(s.View(th, widget.FocusActive))
	if !strings.Contains(out, "↑ more") {
		t.Errorf("tail view should show the ↑ cue; got:\n%s", out)
	}
	if strings.Contains(out, "↓ more") {
		t.Errorf("tail view should NOT show a ↓ cue (already at the bottom); got:\n%s", out)
	}
	// The last line is shown (truncated to width is fine); assert a stable prefix.
	last := sampleStream()[len(sampleStream())-1]
	if prefix := last[:12]; !strings.Contains(out, prefix) {
		t.Errorf("tail view should show the last line (prefix %q); got:\n%s", prefix, out)
	}
}

func TestStreamScrolledMiddleShowsBothCues(t *testing.T) {
	th := theme.Dark()
	s := widget.NewStream(theme.DefaultKeymap())
	s.SetSize(40, 5)
	s.SetLines(sampleStream())
	s.ScrollUp() // off the tail
	s.ScrollUp()
	out := plain(s.View(th, widget.FocusActive))
	if !strings.Contains(out, "↑ more") || !strings.Contains(out, "↓ more") {
		t.Errorf("a scrolled-middle view should show BOTH cues; got:\n%s", out)
	}
}

func TestStreamFitsHeightExactly(t *testing.T) {
	th := theme.Dark()
	s := widget.NewStream(theme.DefaultKeymap())
	h := 5
	s.SetSize(40, h)
	s.SetLines(sampleStream())
	got := strings.Count(s.View(th, widget.FocusActive), "\n") + 1
	if got != h {
		t.Errorf("stream rendered %d rows, want exactly h=%d", got, h)
	}
}

func TestDetailOverflowShowsBottomCue(t *testing.T) {
	th := theme.Dark()
	d := widget.NewDetail(theme.DefaultKeymap())
	d.SetSize(20, 4) // narrow + short: text wraps past 4 rows
	d.SetText(sampleDetail)
	out := plain(d.View(th, widget.FocusActive))
	if !strings.Contains(out, "↓ more") {
		t.Errorf("at the top of overflowing text, the ↓ cue must show; got:\n%s", out)
	}
	if strings.Contains(out, "↑ more") {
		t.Errorf("at the top, there is nothing above; no ↑ cue expected; got:\n%s", out)
	}
}

// --- list cursor bar (regression for the embedded-reset splice defect) ---

func TestListCursorBarPaintsWholeRow(t *testing.T) {
	th := theme.Dark()
	l := widget.NewList(theme.DefaultKeymap(), sampleItems(th)...)
	l.SetSize(24, 8)
	l.SetCursor(0) // cursor on "lena"
	out := l.View(th, widget.FocusActive)
	cursorLine := strings.SplitN(out, "\n", 2)[0]

	// The active cursor bar must paint the title inside one accent style: an SGR
	// opener carrying the accent background (48;2;R;G;B), and NO reset between
	// that opener and the title. The old splice wrapped a pre-styled row, so the
	// title's own \x1b[0m reset blanked the bar before the text — the bug.
	r, g, b := rgb(th.Accent)
	bgFragment := fmt.Sprintf("48;2;%d;%d;%d", r, g, b)
	if !strings.Contains(cursorLine, bgFragment) {
		t.Fatalf("cursor row missing accent bg fragment %q; got:\n%q", bgFragment, cursorLine)
	}
	titleIdx := strings.Index(cursorLine, "lena")
	if titleIdx < 0 {
		t.Fatalf("cursor row missing the title text; got:\n%q", cursorLine)
	}
	bgIdx := strings.Index(cursorLine, bgFragment)
	if bgIdx > titleIdx {
		t.Fatalf("accent bg opens AFTER the title — the title is not in the bar; got:\n%q", cursorLine)
	}
	if strings.Contains(cursorLine[bgIdx:titleIdx], "\x1b[0m") {
		t.Errorf("cursor row resets style before the title (the splice bug); got:\n%q", cursorLine)
	}
}

// rgb extracts the 0-255 RGB components of a hex lipgloss.Color (#rrggbb).
func rgb(c lipgloss.Color) (int, int, int) {
	var r, g, b int
	fmt.Sscanf(string(c), "#%02x%02x%02x", &r, &g, &b)
	return r, g, b
}

// --- width clamping (regression for unclamped list rows) ---

func TestListRowTruncatesNeverWraps(t *testing.T) {
	th := theme.Dark()
	long := strings.Repeat("x", 200)
	l := widget.NewList(theme.DefaultKeymap(), widget.ListItem{Title: long})
	l.SetSize(10, 4)
	for _, focus := range []widget.Focus{widget.FocusIdle, widget.FocusSelected, widget.FocusActive} {
		out := l.View(th, focus)
		if strings.Contains(out, "\n") {
			t.Errorf("focus %d: a single long row wrapped to multiple lines:\n%s", focus, out)
		}
		if w := lipgloss.Width(plain(out)); w > 10 {
			t.Errorf("focus %d: row width %d exceeds the 10-col budget", focus, w)
		}
	}
}

func TestListSetCursorClamps(t *testing.T) {
	th := theme.Dark()
	l := widget.NewList(theme.DefaultKeymap(), sampleItems(th)...)
	l.SetSize(24, 8)
	l.SetCursor(999)
	if got, want := l.Cursor(), len(sampleItems(th))-1; got != want {
		t.Errorf("SetCursor(999) clamped to %d, want %d", got, want)
	}
	l.SetCursor(-5)
	if l.Cursor() != 0 {
		t.Errorf("SetCursor(-5) clamped to %d, want 0", l.Cursor())
	}
}

func TestEmptyWidgetsShowPlaceholders(t *testing.T) {
	th := theme.Dark()
	l := widget.NewList(theme.DefaultKeymap())
	l.SetSize(20, 4)
	if !strings.Contains(plain(l.View(th, widget.FocusSelected)), "(empty)") {
		t.Error("empty list should show a placeholder")
	}
	s := widget.NewStream(theme.DefaultKeymap())
	s.SetSize(20, 4)
	if !strings.Contains(plain(s.View(th, widget.FocusSelected)), "(no messages)") {
		t.Error("empty stream should show a placeholder")
	}
	d := widget.NewDetail(theme.DefaultKeymap())
	d.SetSize(20, 4)
	if !strings.Contains(plain(d.View(th, widget.FocusSelected)), "(nothing selected)") {
		t.Error("empty detail should show a placeholder")
	}
}
