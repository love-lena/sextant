package widget_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/theme"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/widget"
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

// --- box title chip (regression for rune-vs-cell chip measurement) ---

// TestBoxFrameWidthWithWideRuneTitle pins the title chip's measurement in
// display CELLS: a wide-rune title (CJK, emoji — 2 cells per rune) must still
// produce a frame of exactly the requested outer width on every row, whether
// the chip fits or must truncate. The old rune count overfilled the top border
// (and a rune-index cut mis-sized it) for any non-1-cell title.
func TestBoxFrameWidthWithWideRuneTitle(t *testing.T) {
	th := theme.Dark()
	for _, tc := range []struct {
		name  string
		title string
		w, h  int
	}{
		{"cjk_fits", "日本語", 24, 4},
		{"cjk_truncates", "日本語タイトル長い", 12, 4},
		{"emoji", "🚀 launch", 20, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := widget.Box(th, widget.FocusActive, tc.title, th.Accent, "body", tc.w, tc.h)
			lines := strings.Split(out, "\n")
			if len(lines) != tc.h {
				t.Fatalf("box rendered %d rows, want exactly %d:\n%s", len(lines), tc.h, plain(out))
			}
			for i, ln := range lines {
				if got := lipgloss.Width(plain(ln)); got != tc.w {
					t.Errorf("row %d is %d cells wide, want %d: %q", i, got, tc.w, plain(ln))
				}
			}
		})
	}
}

// --- compose widget ---

// typeCompose feeds a string into a Compose widget key by key, returning the
// updated Compose after all keystrokes. The compose must be focused before
// calling this.
func typeCompose(c widget.Compose, text string) widget.Compose {
	for _, r := range text {
		c, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return c
}

// TestComposeWrapsAndShrinkBody pins the core wrap requirement: a line longer
// than the compose width renders on multiple rows (all text visible, none
// horizontally clipped), and the compose height grows accordingly so the body
// shrinks.
//
// The compose is given width=20. The prompt is "> " (2 chars), leaving 18 chars
// of text body per row. Typing 36 chars (2× bodyW) must produce height=2.
// The View must contain all the typed text (nothing lost to horizontal clipping).
func TestComposeWrapsAndShrinkBody(t *testing.T) {
	const width = 20
	c := widget.NewCompose()
	c.SetWidth(width)
	_ = c.Focus() // Focus returns a cursor-blink cmd; irrelevant in tests

	// 36 printable chars: exactly 2× the body width (18). At wrap boundary.
	const text = "abcdefghijklmnopqrstuvwxyz0123456789"
	c = typeCompose(c, text)

	h := c.Height()
	if h < 2 {
		t.Errorf("compose should be ≥2 rows after typing %d chars at width %d; got height %d", len(text), width, h)
	}

	// All typed text must appear in the rendered view (no horizontal clipping).
	view := plain(c.View(theme.Dark(), widget.FocusActive))
	if !strings.Contains(view, "abcdefghijklmnopqr") {
		t.Errorf("first 18 chars missing from compose view (horizontally clipped?):\n%s", view)
	}
	if !strings.Contains(view, "stuvwxyz0123456789") {
		t.Errorf("last 18 chars missing from compose view (not wrapped to next row?):\n%s", view)
	}
}

// TestComposeCapAtMaxRows pins that a compose taller than ComposeMaxRows is
// capped: typing more text than fits in ComposeMaxRows rows returns exactly
// ComposeMaxRows from Height() (never more), and the view stays at the cap.
func TestComposeCapAtMaxRows(t *testing.T) {
	const width = 20 // bodyW = 18
	c := widget.NewCompose()
	c.SetWidth(width)
	_ = c.Focus()

	// Type (ComposeMaxRows+2) × bodyW chars: should overflow the cap.
	bodyW := width - 2 // prompt is "> " = 2 chars
	overflow := (widget.ComposeMaxRows + 2) * bodyW
	text := strings.Repeat("x", overflow)
	c = typeCompose(c, text)

	h := c.Height()
	if h != widget.ComposeMaxRows {
		t.Errorf("compose height = %d, want exactly ComposeMaxRows = %d (text taller than cap should scroll, not grow)", h, widget.ComposeMaxRows)
	}

	// All characters must be in the buffer (not truncated — just scrolled within
	// the capped viewport). Check value length, not the rendered view (the view
	// only shows ComposeMaxRows rows).
	if got := c.Value(); len([]rune(got)) < overflow {
		t.Errorf("compose truncated input: got %d runes, want ≥ %d", len([]rune(got)), overflow)
	}
}

// TestComposeHeightMatchesTextareaWrap pins Height() against the textarea's REAL
// word-aware wrap, not a character-packing estimate. The two diverge on text
// with spaces: word wrap moves whole words to the next row (more rows than
// ceil(chars/width)), so an underestimated height makes the textarea scroll to
// the cursor and the HEAD of the draft silently disappears — found live, typing
// an ordinary sentence into a DM. For a buffet of sentences and widths, the
// head of the draft must stay visible whenever the content fits under the cap,
// and the trailing cursor row the textarea reserves must be accounted for.
// This test is the loud-failure tripwire if a bubbles upgrade changes wrap().
func TestComposeHeightMatchesTextareaWrap(t *testing.T) {
	cases := []struct {
		name  string
		width int
		text  string
	}{
		// Three 10-char words at bodyW=18: char-packing says ceil(32/18)=2 rows,
		// the word-aware wrap needs 3 (each word straddles a boundary and moves
		// down whole, leaving ~8 cells of slack per row). THE divergent case —
		// the old estimate set height 2, the textarea scrolled, the head vanished.
		{"words_leave_slack", 20, "aaaaaaaaaa bbbbbbbbbb cccccccccc"},
		{"dogfood_sentence", 40, "this is my third dash iteration, oh god where did the rest of my message go?"},
		{"word_straddles_boundary", 20, "wrap on word boundaries pushes whole words down"},
		{"short_words", 16, "a bb ccc dddd eeeee ffffff ggggggg"},
		{"exact_fill_reserves_cursor_row", 12, "abcdefghij"},
		{"single_word", 30, "hello"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := widget.NewCompose()
			c.SetWidth(tc.width)
			_ = c.Focus()
			c = typeCompose(c, tc.text)

			if c.Height() > widget.ComposeMaxRows {
				t.Fatalf("height %d exceeds the cap %d", c.Height(), widget.ComposeMaxRows)
			}
			view := plain(c.View(theme.Dark(), widget.FocusActive))
			firstWord := strings.Fields(tc.text)[0]
			lastWord := tc.text[strings.LastIndex(tc.text, " ")+1:]
			if c.Height() < widget.ComposeMaxRows {
				// Under the cap nothing may scroll away: head AND tail visible.
				if !strings.Contains(view, firstWord) {
					t.Errorf("head of the draft scrolled out of view (height %d under-counts the textarea's wrap); view:\n%s", c.Height(), view)
				}
			}
			if !strings.Contains(view, lastWord) {
				t.Errorf("tail of the draft not visible (cursor row missing); view:\n%s", view)
			}
		})
	}
}

// TestComposeBlurredViewMatchesHeight pins the Height()/View() contract across
// blur: a blurred compose holding a multi-line draft must still render exactly
// Height() rows. Blur retains the draft (so Height() keeps the draft's row
// count) but renders the one-line hint — unpadded, that left hosts which
// subtract Height() from their body (surface/stream.go relayout) with a dead
// zone of up to ComposeMaxRows-1 rows, since a blur does not relayout.
func TestComposeBlurredViewMatchesHeight(t *testing.T) {
	const width = 20 // bodyW = 18
	c := widget.NewCompose()
	c.SetWidth(width)
	_ = c.Focus()
	c = typeCompose(c, strings.Repeat("x", 3*(width-2))) // 3 full rows + cursor row
	c.Blur()

	h := c.Height()
	if h < 2 {
		t.Fatalf("multi-line draft should keep Height() > 1 across blur; got %d", h)
	}
	for _, focus := range []widget.Focus{widget.FocusIdle, widget.FocusSelected} {
		view := c.View(theme.Dark(), focus)
		if got := strings.Count(view, "\n") + 1; got != h {
			t.Errorf("focus %d: blurred View renders %d rows; Height() = %d (host dead zone)", focus, got, h)
		}
	}
}

// TestComposeChunkSpellingABindingIsText pins the burst/paste guard at the
// WIDGET level: the textarea string-matches its own editing bindings, so a
// multi-rune chunk whose String() spells one ("right", "up", "end") would
// execute as an editing command and the text would silently vanish. (Found
// live: the word "right" disappeared from a sentence delivered as a burst.)
// Compose must insert chunks directly — every word lands, including the
// binding-spelling ones.
func TestComposeChunkSpellingABindingIsText(t *testing.T) {
	c := widget.NewCompose()
	c.SetWidth(60)
	_ = c.Focus()

	chunk := func(s string) tea.KeyMsg {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	// Delivered the way a terminal burst arrives: arbitrary chunk boundaries,
	// some chunks spelling textarea editing bindings.
	for _, piece := range []string{"its all ", "right", " here ", "up", " and ", "end", " to ", "down"} {
		c, _ = c.Update(chunk(piece))
	}
	const want = "its all right here up and end to down"
	if got := c.Value(); got != want {
		t.Errorf("chunks were eaten as editing commands:\n got %q\nwant %q", got, want)
	}
}

// TestPastedChunkNeverScrolls pins the chunk guard at the widget's own binding
// matches: pasted text is content, so a chunk spelling a scroll binding ("up",
// "down") — or a single-character bracketed paste of one ("k", "j"), which a
// length guard alone would miss — must not move a Stream's or a List's view.
func TestPastedChunkNeverScrolls(t *testing.T) {
	chunk := func(s string) tea.KeyMsg {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	paste := func(s string) tea.KeyMsg {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Paste: true}
	}

	t.Run("stream", func(t *testing.T) {
		s := widget.NewStream(theme.DefaultKeymap())
		s.SetSize(40, 4)
		s.SetLines(sampleStream()) // 8 lines into 4 rows: scrollable, following
		for _, msg := range []tea.KeyMsg{chunk("up"), paste("k"), paste("up")} {
			s, _ = s.Update(msg)
			if !s.Following() {
				t.Fatalf("pasted %q scrolled the stream off the tail", msg.Runes)
			}
		}
		// The real keystroke still scrolls — the guard blocks chunks, not keys.
		s, _ = s.Update(tea.KeyMsg{Type: tea.KeyUp})
		if s.Following() {
			t.Error("a real Up keystroke should still scroll")
		}
	})

	t.Run("list", func(t *testing.T) {
		l := widget.NewList(theme.DefaultKeymap(),
			widget.ListItem{Title: "row0"}, widget.ListItem{Title: "row1"}, widget.ListItem{Title: "row2"})
		l.SetSize(40, 3)
		for _, msg := range []tea.KeyMsg{chunk("down"), paste("j"), paste("down")} {
			l, _ = l.Update(msg)
			if l.Cursor() != 0 {
				t.Fatalf("pasted %q moved the list cursor to %d", msg.Runes, l.Cursor())
			}
		}
		l, _ = l.Update(tea.KeyMsg{Type: tea.KeyDown})
		if l.Cursor() != 1 {
			t.Error("a real Down keystroke should still move the cursor")
		}
	})
}
