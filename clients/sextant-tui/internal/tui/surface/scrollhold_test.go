package surface_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/surface"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/theme"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/widget"
)

// These tests pin ADR-0026's panes-hold-their-place through the stream's full
// rebuilds: a width change (pane toggle, preset cycle, resize) and a theme
// switch replay the entry log, and that replay must not yank a scrolled-back
// reader to the tail. The scroll state survives exactly; the position is
// entry-anchored — the entry that was topmost before the replay has its first
// rendered line topmost after (stable across a rewrap; sub-entry position
// through a rewrap is deliberately approximate).

// newScrollStream builds a focused, non-compose stream at the given inner size,
// fed n short messages msg-000..msg-(n-1).
func newScrollStream(t *testing.T, w, h, n int) *surface.Stream {
	t.Helper()
	s := surface.NewStream(context.Background(), nil, "msg.topic.plan", theme.Dark(), theme.DefaultKeymap(), surface.WithAuthors(sampleAuthors()))
	s.SetSize(w, h)
	s.SetFocus(widget.FocusActive)
	for i := range n {
		s.Update(chatEvent("lena", fmt.Sprintf("msg-%03d", i)))
	}
	return s
}

// scrollUp sends n Up keys to the active stream (releasing tail-follow).
func scrollUp(s *surface.Stream, n int) {
	for range n {
		s.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
}

// viewRows returns the stream's rendered rows with ANSI stripped.
func viewRows(s *surface.Stream) []string {
	return strings.Split(ansi.Strip(s.View()), "\n")
}

// TestStreamScrollbackSurvivesRewrap: a width change re-wraps every logged
// entry, so absolute line numbers shift — the view must re-anchor on the ENTRY
// that was topmost, not the line number. A long message above the anchor wraps
// to 3 lines at the narrow width and 1 line at the wide width; a line-anchored
// view would land two messages further down.
func TestStreamScrollbackSurvivesRewrap(t *testing.T) {
	s := surface.NewStream(context.Background(), nil, "msg.topic.plan", theme.Dark(), theme.DefaultKeymap(), surface.WithAuthors(sampleAuthors()))
	s.SetSize(40, 6) // body width 25: the long message wraps to 3 lines
	s.SetFocus(widget.FocusActive)
	for i := range 4 {
		s.Update(chatEvent("lena", fmt.Sprintf("msg-%03d", i)))
	}
	s.Update(chatEvent("lena", "msg-004 "+strings.Repeat("wrap ", 9)+"wrap"))
	for i := 5; i < 12; i++ {
		s.Update(chatEvent("lena", fmt.Sprintf("msg-%03d", i)))
	}

	scrollUp(s, 1) // off the tail: msg-006 is now the topmost visible entry
	rows := viewRows(s)
	if !strings.Contains(rows[1], "msg-006") {
		t.Fatalf("precondition: msg-006 topmost after the scroll, got %q", rows[1])
	}

	s.SetSize(80, 6) // rewrap: the long message folds to 1 line; lines shift up

	rows = viewRows(s)
	if !strings.HasPrefix(rows[0], "↑ more") {
		t.Fatalf("the rewrap yanked a scrolled-back view (no top cue): rows %q", rows)
	}
	if !strings.Contains(rows[1], "msg-006") {
		t.Errorf("the anchored entry should stay topmost across the rewrap, got %q (line-anchored would land on msg-008)", rows[1])
	}
}

// TestStreamScrollbackSurvivesRetheme: a theme switch replays the log without
// changing the wrap, so a scrolled-back view holds its exact position — the
// stripped render is identical before and after.
func TestStreamScrollbackSurvivesRetheme(t *testing.T) {
	s := newScrollStream(t, 40, 6, 12)
	scrollUp(s, 2)
	before := ansi.Strip(s.View())
	if !strings.HasPrefix(before, "↑ more") {
		t.Fatalf("precondition: scrolled back, got:\n%s", before)
	}

	s.SetTheme(theme.Light())

	if after := ansi.Strip(s.View()); after != before {
		t.Errorf("a retheme moved a scrolled-back view:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestStreamFollowSurvivesReplay: a tail-following view keeps following through
// a width change and a theme switch — new messages still pin to the bottom.
func TestStreamFollowSurvivesReplay(t *testing.T) {
	s := newScrollStream(t, 40, 6, 12)

	s.SetSize(80, 6) // width change: replay while following
	s.Update(chatEvent("lena", "msg-012"))
	if out := ansi.Strip(s.View()); !strings.Contains(out, "msg-012") {
		t.Errorf("a following view should track the tail after a rewrap, got:\n%s", out)
	}

	s.SetTheme(theme.Light()) // retheme: replay while following
	s.Update(chatEvent("lena", "msg-013"))
	if out := ansi.Strip(s.View()); !strings.Contains(out, "msg-013") {
		t.Errorf("a following view should track the tail after a retheme, got:\n%s", out)
	}
}

// TestStreamScrollbackAnchorTrimmed: when the trim drops the anchored entry
// itself, the view clamps to the oldest surviving entry — no panic, still
// scrolled back, the trim marker one row above.
func TestStreamScrollbackAnchorTrimmed(t *testing.T) {
	old := surface.MaxStreamEntries
	surface.MaxStreamEntries = 10
	t.Cleanup(func() { surface.MaxStreamEntries = old })

	s := newScrollStream(t, 40, 6, 10)
	scrollUp(s, 4) // anchor near the top: msg-001 is the topmost entry
	rows := viewRows(s)
	if !strings.Contains(rows[1], "msg-001") {
		t.Fatalf("precondition: msg-001 topmost, got %q", rows[1])
	}

	// The next message pushes the log over the cap; the trim drops the two
	// oldest entries — including the anchor.
	s.Update(chatEvent("lena", "msg-010"))

	rows = viewRows(s)
	if !strings.HasPrefix(rows[0], "↑ more") {
		t.Fatalf("the trim yanked a scrolled-back view (no top cue): rows %q", rows)
	}
	if !strings.Contains(rows[1], "msg-002") {
		t.Errorf("a trimmed-away anchor should clamp to the oldest surviving entry, got %q", rows[1])
	}
	// One more step up reaches the honest trim marker above the oldest entry —
	// the buffer's very top, so there is no ↑ cue and the marker is the first row.
	scrollUp(s, 1)
	if rows = viewRows(s); !strings.Contains(rows[0], "older history trimmed") {
		t.Errorf("the trim marker should sit just above the oldest entry, got %q", rows[0])
	}
}
