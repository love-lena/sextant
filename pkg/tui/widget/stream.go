package widget

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/pkg/tui/theme"
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

// SetSize sets the inner content area (inside any box chrome) and re-pins the
// tail when following.
func (s *Stream) SetSize(w, h int) {
	s.width, s.height = w, h
	if s.follow {
		s.pinTail()
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

// SetLines replaces the whole buffer and pins to the tail.
func (s *Stream) SetLines(lines []string) {
	s.lines = lines
	s.follow = true
	s.pinTail()
}

// Following reports whether the view is tracking the tail.
func (s Stream) Following() bool { return s.follow }

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

// maxOffset is the largest top-line index that still fills the viewport.
func (s Stream) maxOffset() int {
	if s.height <= 0 {
		return 0
	}
	return max(0, len(s.lines)-s.height)
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
	h := s.height
	if h <= 0 {
		h = 1
	}
	if len(s.lines) == 0 {
		return lipgloss.NewStyle().Foreground(t.Dim).Width(w).Render("(no messages)")
	}

	end := s.offset + h
	if end > len(s.lines) {
		end = len(s.lines)
	}
	visible := s.lines[s.offset:end]

	// Overflow cues: a brighter accent when active, dim otherwise.
	cueHue := t.Dim
	if focus == FocusActive {
		cueHue = t.Accent
	}
	cue := func(text string) string {
		return lipgloss.NewStyle().Foreground(cueHue).Width(w).MaxWidth(w).Render(text)
	}

	var rows []string
	if s.offset > 0 {
		rows = append(rows, cue("↑ more"))
	}
	for _, ln := range visible {
		rows = append(rows, lipgloss.NewStyle().Foreground(t.Fg).MaxWidth(w).Render(ln))
	}
	if end < len(s.lines) {
		rows = append(rows, cue("↓ more"))
	}
	// Trim to height: drop interior lines if the cues pushed us over.
	if len(rows) > h {
		rows = rows[:h]
	}
	return strings.Join(rows, "\n")
}
