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
// focus it was granted, and records every key delivered to it (its "content
// state" — so a test can assert that focus moves never touch it and that
// content keys reach the focused pane). It has no SDK and no bus, which is the
// point — the layout composes against the Surface contract alone, so a mock
// satisfies it and the layout never knows the difference. It proves the layout
// is domain-free.
type mockSurface struct {
	id, title string
	w, h      int
	focus     widget.Focus
	stopped   int
	// themed records the variant of the last theme the layout pushed in via
	// SetTheme, so a test can assert a runtime theme switch reaches the surface.
	themed theme.Variant

	// capturing is what CapturingText reports — a test sets it to model a live
	// compose, so the layout's q-vs-typing gate can be asserted.
	capturing bool
	// keys records every key delivered to Update, in order — the mock's content
	// state. A focus move must never append to it.
	keys []string
}

func newMock(id, title string) *mockSurface {
	return &mockSurface{id: id, title: title}
}

func (s *mockSurface) ID() string    { return s.id }
func (s *mockSurface) Title() string { return s.title }

func (s *mockSurface) SetSize(w, h int)        { s.w, s.h = w, h }
func (s *mockSurface) SetFocus(f widget.Focus) { s.focus = f }
func (s *mockSurface) CapturingText() bool     { return s.capturing }
func (s *mockSurface) SetTheme(t theme.Theme)  { s.themed = t.Variant }
func (s *mockSurface) Init() tea.Cmd           { return nil }
func (s *mockSurface) Stop()                   { s.stopped++ }

// Update records a delivered key — the observable content mutation the
// focus-move tests assert never happens on a focus key.
func (s *mockSurface) Update(msg tea.Msg) tea.Cmd {
	if km, ok := msg.(tea.KeyMsg); ok {
		s.keys = append(s.keys, km.String())
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
