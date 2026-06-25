package widget

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/theme"
)

// Stream is an append-and-scroll viewport: lines are appended at the bottom and
// the view follows the tail until the operator scrolls up, at which point follow
// releases and the view holds its position. It is the read-stream widget the
// message surface builds on (ADR-0023). Like the other widgets it is a Bubble
// Tea component rendering only from a theme.Theme and its Focus.
//
// A scroll cue (↑ more above / ↓ more below) marks overflow at the edges, so the
// operator can tell at a glance when content runs past the viewport.
type Stream struct {
	keys  theme.Keymap
	lines []string
	// offset is the index of the top visible line.
	offset int
	// follow is true while the view tracks the tail; scrolling up releases it,
	// scrolling back to the bottom re-engages it.
	follow        bool
	width, height int
}

// NewStream builds an empty Stream that follows the tail.
func NewStream(keys theme.Keymap) Stream {
	return Stream{keys: keys, follow: true}
}

// SetSize sets the inner content area (inside any box chrome): re-pinning the
// tail when following, clamping a scrolled-back offset into the new range
// otherwise (the view holds its place through a resize).
func (s *Stream) SetSize(w, h int) {
	s.width, s.height = w, h
	if s.follow {
		s.pinTail()
		return
	}
	if s.offset > s.maxOffset() {
		s.offset = s.maxOffset()
	}
}

// Append adds lines to the bottom. While following, the view stays pinned to the
// tail; once the operator has scrolled up, new lines do not yank the view back.
func (s *Stream) Append(lines ...string) {
	s.lines = append(s.lines, lines...)
	if s.follow {
		s.pinTail()
	}
}

// SetLines replaces the whole buffer, holding the scroll state (ADR-0026:
// panes hold their place): while following it re-pins to the tail; scrolled
// back it clamps the offset into the new range and stays scrolled back, so a
// re-render (a retheme, a rewrap) never yanks the operator off their position.
// A caller that wants to move the view positions it explicitly with ScrollTo.
func (s *Stream) SetLines(lines []string) {
	s.lines = lines
	if s.follow {
		s.pinTail()
		return
	}
	if s.offset > s.maxOffset() {
		s.offset = s.maxOffset()
	}
}

// Following reports whether the view is tracking the tail.
func (s Stream) Following() bool { return s.follow }

// Offset returns the index of the top visible line, so a caller about to
// re-render the buffer (the surface's replay) can read the position it must
// re-anchor before the lines change under it.
func (s Stream) Offset() int { return s.offset }

// ScrollTo puts line at the top of the view, clamped into the valid range.
// Landing at (or past) the tail re-engages follow — the same rule ScrollDown
// applies when the operator reaches the bottom; anywhere above it the view is
// scrolled back and holds.
func (s *Stream) ScrollTo(line int) {
	if line < 0 {
		line = 0
	}
	if line > s.maxOffset() {
		line = s.maxOffset()
	}
	s.offset = line
	s.follow = s.offset >= s.maxOffset()
}

// Init implements tea.Model. The stream has no startup command.
func (s Stream) Init() tea.Cmd { return nil }

// Update scrolls on the keymap's Up/Down bindings. It is a no-op for other
// messages; route keys here only when the stream is active.
func (s Stream) Update(msg tea.Msg) (Stream, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch {
		case keyMatches(km, s.keys.Up):
			s.ScrollUp()
		case keyMatches(km, s.keys.Down):
			s.ScrollDown()
		}
	}
	return s, nil
}

// ScrollUp moves the view up one line and releases tail-follow.
func (s *Stream) ScrollUp() {
	if s.offset > 0 {
		s.offset--
		s.follow = false
	}
}

// ScrollDown moves the view down one line; reaching the bottom re-engages
// tail-follow.
func (s *Stream) ScrollDown() {
	if s.offset < s.maxOffset() {
		s.offset++
	}
	if s.offset >= s.maxOffset() {
		s.follow = true
	}
}

// maxOffset is the largest top-line index that still shows the tail, in step
// with renderViewport's cue reservation.
func (s Stream) maxOffset() int {
	return maxViewportOffset(len(s.lines), s.height)
}

// pinTail sets the offset so the last line is visible at the bottom.
func (s *Stream) pinTail() { s.offset = s.maxOffset() }

// View renders the visible window for the given focus. An accent border (drawn
// by the surrounding Box) carries selected/active; within the stream, the active
// state is cued by a brighter overflow marker. Overflow above/below is marked
// with ↑/↓ cues. An empty stream renders a dim placeholder.
func (s Stream) View(t theme.Theme, focus Focus) string {
	w := s.width
	if w <= 0 {
		w = 1
	}
	if len(s.lines) == 0 {
		return lipgloss.NewStyle().Foreground(t.Dim).Width(w).Render("(no messages)")
	}
	return renderViewport(t, focus, s.lines, s.offset, s.width, s.height)
}
