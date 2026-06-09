package layout_test

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/pkg/tui/surface"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// mockSurface is a trivial, deterministic Surface for the layout tests and the
// gallery: it renders fixed text labelled with its id, tracks the last size and
// focus it was granted, and emits the intents a test drives it to emit. It has
// no SDK and no bus, which is the point — the layout composes against the
// Surface contract alone, so a mock satisfies it and the layout never knows the
// difference. It proves the layout is domain-free.
type mockSurface struct {
	id, title string
	w, h      int
	focus     widget.Focus
	stopped   int
	// themed records the variant of the last theme the layout pushed in via
	// SetTheme, so a test can assert a runtime theme switch reaches the surface.
	themed theme.Variant

	// done records the surface's chosen done id (defaults to its own id).
	doneID string
}

func newMock(id, title string) *mockSurface {
	return &mockSurface{id: id, title: title, doneID: id}
}

func (s *mockSurface) ID() string    { return s.id }
func (s *mockSurface) Title() string { return s.title }

func (s *mockSurface) SetSize(w, h int)        { s.w, s.h = w, h }
func (s *mockSurface) SetFocus(f widget.Focus) { s.focus = f }
func (s *mockSurface) SetTheme(t theme.Theme)  { s.themed = t.Variant }
func (s *mockSurface) Init() tea.Cmd           { return nil }
func (s *mockSurface) Stop()                   { s.stopped++ }

// Update emits DoneMsg on Esc while active — the Surface contract's step-out
// (the layout never steps out on Back itself; it honours the surface's intent).
func (s *mockSurface) Update(msg tea.Msg) tea.Cmd {
	if km, ok := msg.(tea.KeyMsg); ok && s.focus == widget.FocusActive && km.String() == "esc" {
		id := s.doneID
		return func() tea.Msg { return surface.DoneMsg{ID: id} }
	}
	return nil
}

// View renders deterministic content: the id, the granted inner size, and the
// focus state, so a golden shows the layout placed and sized the pane correctly.
func (s *mockSurface) View() string {
	state := []string{"idle", "selected", "active"}[s.focus]
	body := fmt.Sprintf("%s\nsize %dx%d\n%s", s.id, s.w, s.h, state)
	// A few filler lines so the body reads as content, not a stub.
	body += strings.Repeat("\n· · ·", 2)
	return body
}

var _ surface.Surface = (*mockSurface)(nil)
