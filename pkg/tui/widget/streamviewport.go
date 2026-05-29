package widget

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// StreamViewport is a scrollback pager over bubbles/viewport with three
// additions every tailing surface needs: tail/autoscroll (stick to the
// bottom unless the operator scrolled up), a ring-buffer line cap (so an
// overnight tail can't grow unbounded), and explicit g/G top/bottom keys.
//
// Pointer receiver, mutates in place (see package doc).
type StreamViewport struct {
	vp        viewport.Model
	lines     []string
	maxLines  int // 0 = unbounded
	following bool
	ready     bool
}

// NewStreamViewport returns a viewport that keeps at most maxLines lines
// (0 = unbounded) and starts in follow mode.
func NewStreamViewport(maxLines int) *StreamViewport {
	return &StreamViewport{maxLines: maxLines, following: true}
}

// SetSize informs the viewport of its content rect.
func (s *StreamViewport) SetSize(w, h int) {
	if !s.ready {
		s.vp = viewport.New(w, h)
		s.ready = true
	} else {
		s.vp.Width = w
		s.vp.Height = h
	}
	s.render()
	if s.following {
		s.vp.GotoBottom()
	}
}

// SetContent replaces the buffer (one-shot dump).
func (s *StreamViewport) SetContent(lines []string) {
	s.lines = append(s.lines[:0], lines...)
	s.trim()
	s.render()
	if s.following {
		s.vp.GotoBottom()
	}
}

// Append adds lines (tail). Autoscrolls only while following.
func (s *StreamViewport) Append(lines ...string) {
	s.lines = append(s.lines, lines...)
	s.trim()
	s.render()
	if s.following {
		s.vp.GotoBottom()
	}
}

// Update forwards scroll messages to the viewport, intercepting g/G for
// top/bottom, and reconciles the follow flag from the resulting position.
func (s *StreamViewport) Update(msg tea.Msg) tea.Cmd {
	if !s.ready {
		return nil
	}
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "G", "end":
			s.vp.GotoBottom()
			s.following = true
			return nil
		case "g", "home":
			s.vp.SetYOffset(0)
			s.following = false
			return nil
		}
	}
	var cmd tea.Cmd
	s.vp, cmd = s.vp.Update(msg)
	s.following = s.vp.AtBottom()
	return cmd
}

// View renders the visible window.
func (s *StreamViewport) View() string {
	if !s.ready {
		return ""
	}
	return s.vp.View()
}

// Following reports whether new Appends will autoscroll.
func (s *StreamViewport) Following() bool { return s.following }

// SetFollow forces follow mode on/off. Top-anchored consumers (e.g.
// DetailPane) set it false so SetContent stays at the top.
func (s *StreamViewport) SetFollow(f bool) { s.following = f }

// LineCount returns the number of buffered lines (post-trim).
func (s *StreamViewport) LineCount() int { return len(s.lines) }

func (s *StreamViewport) render() {
	if !s.ready {
		return
	}
	s.vp.SetContent(strings.Join(s.lines, "\n"))
}

func (s *StreamViewport) trim() {
	if s.maxLines > 0 && len(s.lines) > s.maxLines {
		s.lines = s.lines[len(s.lines)-s.maxLines:]
	}
}
