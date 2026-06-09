// Command dash-layoutgallery is a preview binary for the dash's layout engine.
// It wires trivial MOCK surfaces — fixed-text panes with an id and a title, no
// SDK and no bus — into the layout.Model, so the cockpit can be driven (preset
// switch, pane toggle, reflow) without a running bus. The mocks stand in for the
// three browsers (ADR-0024: clients · topics · artifacts, side by side); this
// also proves the layout composes against the Surface contract alone: the mocks
// satisfy the interface and the layout never knows the difference.
//
// Run: go run ./cmd/dash-layoutgallery [--theme light|dark|auto]
//
// Keys (from the locked keymap + layout shortcuts):
//
//	↑↓←→/hjkl  move the selected pane (accent border)
//	enter      step into the selected pane (it goes active)
//	esc        step out
//	p          cycle the preset (cockpit → split)
//	o          open the options menu (toggle panes, switch preset/theme, quit)
//	q / ctrl+c quit
//
// It is a dev affordance, not part of the dash. The real dash binary wires the
// domain surfaces and the identity; this gallery proves the layout mechanics.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/pkg/tui/layout"
	"github.com/love-lena/sextant/pkg/tui/surface"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

func main() {
	themeFlag := flag.String("theme", "auto", "theme: light, dark, or auto")
	flag.Parse()

	cfg := layout.DefaultConfig()
	cfg.Theme = resolveTheme(*themeFlag).Variant

	m := layout.New(resolveTheme(*themeFlag), theme.DefaultKeymap(), cfg,
		newMock("clients", "Clients", clientsBody),
		newMock("topics", "Topics", topicsBody),
		newMock("artifacts", "Artifacts", artifactsBody))

	p := tea.NewProgram(root{m: m}, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "layout gallery error:", err)
		os.Exit(1)
	}
}

func resolveTheme(name string) theme.Theme {
	switch name {
	case "light":
		return theme.Light()
	case "dark":
		return theme.Dark()
	default:
		return theme.Auto()
	}
}

// root adapts the layout.Model (which has a value-receiver Update returning a
// Model) to the tea.Model interface (Update returns a tea.Model). It forwards
// every message into the layout — the gallery host owns nothing else.
type root struct{ m layout.Model }

func (r root) Init() tea.Cmd { return r.m.Init() }

func (r root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	r.m, cmd = r.m.Update(msg)
	return r, cmd
}

func (r root) View() string { return r.m.View() }

// --- mock surface ---

// mockSurface is a trivial deterministic pane: fixed body text plus a live
// readout of the id, granted size, and focus state, so the gallery visibly shows
// the layout placing and sizing each pane. It has no SDK and no bus.
type mockSurface struct {
	id, title string
	body      string
	w, h      int
	focus     widget.Focus
}

func newMock(id, title, body string) *mockSurface {
	return &mockSurface{id: id, title: title, body: body}
}

func (s *mockSurface) ID() string              { return s.id }
func (s *mockSurface) Title() string           { return s.title }
func (s *mockSurface) SetSize(w, h int)        { s.w, s.h = w, h }
func (s *mockSurface) SetFocus(f widget.Focus) { s.focus = f }
func (s *mockSurface) Init() tea.Cmd           { return nil }
func (s *mockSurface) Stop()                   {}

// SetTheme satisfies the Surface contract's re-theme hook. The mock renders only
// plain text (no hues of its own — the Box chrome carries the theme), so there is
// nothing for it to re-resolve; the layout's theme toggle re-themes the chrome
// around it regardless.
func (s *mockSurface) SetTheme(theme.Theme) {}

// Update emits DoneMsg on Esc while active — the surface-driven step-out the
// layout honours.
func (s *mockSurface) Update(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok || s.focus != widget.FocusActive {
		return nil
	}
	if km.String() == "esc" {
		id := s.id
		return func() tea.Msg { return surface.DoneMsg{ID: id} }
	}
	return nil
}

func (s *mockSurface) View() string {
	state := []string{"idle", "selected", "active"}[s.focus]
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", s.body)
	fmt.Fprintf(&b, "[%s · %dx%d]\n", state, s.w, s.h)
	if s.focus == widget.FocusActive {
		b.WriteString("esc to step out")
	}
	return b.String()
}

const (
	clientsBody   = "every issued identity\n● lena  ● coordinator-1  ○ agent-beta\nenter a row → its DM, in place"
	topicsBody    = "every topic with messages\nplan · build · review\nenter a row → its conversation, in place"
	artifactsBody = "every artifact in the bucket\ndash-plan rev 3 · notes rev 12\nenter a row → its reader, in place"
)

var _ surface.Surface = (*mockSurface)(nil)
