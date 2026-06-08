// Command dash-layoutgallery is a preview binary for the dash's layout engine
// (TASK-7.4). It wires trivial MOCK surfaces — fixed-text panes with an id and a
// title, no SDK and no bus — into the layout.Model, so the cockpit can be driven
// (preset switch, pane toggle, reflow, detail-on-demand) without a running bus.
// This also proves the layout composes against the Surface contract alone: the
// mocks satisfy the interface and the layout never knows the difference.
//
// Run: go run ./cmd/dash-layoutgallery [--theme light|dark|auto]
//
// Keys (from the locked keymap + layout shortcuts):
//
//	↑↓←→/hjkl  move the selected pane (accent border)
//	enter      step into the selected pane (it goes active)
//	esc        step out (and close the detail pane)
//	p          cycle the preset (cockpit → stream → split)
//	d          toggle the detail-on-demand pane in/out
//	o          open the options menu (toggle panes, switch preset/theme, quit)
//	q / ctrl+c quit
//
// It is a dev affordance, not part of the dash. The real dash binary (7.5) wires
// the domain surfaces and the identity; this gallery proves the layout mechanics.
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
		newMock("presence", "presence", presenceBody),
		newMock("stream", "stream", streamBody),
		newMock("artifact", "artifact", artifactBody),
		newMock("detail", "detail", detailBody))

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
// Model) to the tea.Model interface (Update returns a tea.Model). It is also the
// HOST end of the detail-on-demand contract: a surface's OpenMsg flows into the
// layout, which opens detail and emits a layout.DetailOpenedMsg for the host to
// retarget the detail content. The gallery reads that notification (a no-op,
// since its detail mock is static) and otherwise forwards every message into the
// layout — safe because DetailOpenedMsg is a distinct type the layout ignores, so
// there is no re-trigger loop.
type root struct{ m layout.Model }

func (r root) Init() tea.Cmd { return r.m.Init() }

func (r root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if opened, ok := msg.(layout.DetailOpenedMsg); ok {
		// The real dash (7.5) retargets its detail surface onto opened.Ref here. The
		// gallery's detail mock is static, so this is a no-op acknowledgement; the
		// layout has already shown + focused the detail pane.
		_ = opened
		return r, nil
	}
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

// Update emits the OpenMsg intent when the operator presses "x" while this pane
// is active, so the gallery can demonstrate detail-on-demand opening from a
// surface intent (the stream "opens an artifact in detail"). Esc emits DoneMsg,
// the surface-driven step-out the layout honours.
func (s *mockSurface) Update(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok || s.focus != widget.FocusActive {
		return nil
	}
	switch km.String() {
	case "x":
		return func() tea.Msg { return surface.OpenMsg{Kind: surface.OpenArtifact, Ref: "design-doc"} }
	case "esc":
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
	if s.focus == widget.FocusActive && s.id != "detail" {
		b.WriteString("press x to open detail · esc to step out")
	}
	return b.String()
}

const (
	presenceBody = "the clients directory\n● lena  ● coordinator-1  ○ agent-beta"
	streamBody   = "one read-stream + an optional compose\nlena: let's get the dash building"
	artifactBody = "a document reader/review\n# Dash build plan"
	detailBody   = "detail-on-demand: hidden + toggled,\nnever an always-on column"
)

var _ surface.Surface = (*mockSurface)(nil)
