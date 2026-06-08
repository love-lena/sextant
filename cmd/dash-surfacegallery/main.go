// Command dash-surfacegallery is a preview binary for the dash's pane-surfaces.
// It runs each surface — presence, the message stream (with compose), and the
// artifact reader/review — standalone, against SEEDED mock data so the demo is
// deterministic and needs no bus. It proves the Surface contract works in
// isolation (a surface is its own tea.Program) AND that the same surface type
// mounts as a pane unchanged: the host wraps the surface's inner View in the
// shared widget.Box, exactly as the dash's layout will.
//
// Run: go run ./cmd/dash-surfacegallery [--surface presence|stream|artifact]
//
//	[--theme light|dark|auto]
//
// Keys: tab cycles surfaces · enter steps into the focused surface · esc steps
// out · ↑/↓ move within an active surface · (stream/artifact, when active) type
// to compose · t toggles theme · q / ctrl+c quits.
//
// It is a dev affordance, not part of the dash.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/tui/busfeed"
	"github.com/love-lena/sextant/pkg/tui/surface"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
	"github.com/love-lena/sextant/pkg/wire"
)

func main() {
	surfaceFlag := flag.String("surface", "", "show only one surface: presence, stream, or artifact (default: all, tab to cycle)")
	themeFlag := flag.String("theme", "auto", "theme: light, dark, or auto")
	flag.Parse()

	m := newModel(resolveTheme(*themeFlag), *surfaceFlag)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "surface gallery error:", err)
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

// pane bundles a surface with its seeding closure, so the gallery can run each
// against mock data without a bus. The seed feeds the surface's own load/event
// messages — the same messages its fetch/feed would produce live — proving the
// surface renders purely from those.
type pane struct {
	s    surface.Surface
	seed func(surface.Surface)
}

// model hosts the surfaces and tracks which one is shown and whether the operator
// has stepped in. It demonstrates the two-level focus model and that a surface
// mounts unchanged inside widget.Box.
type model struct {
	th    theme.Theme
	keys  theme.Keymap
	panes []pane
	only  bool // a single surface (no cycling)

	sel    int
	active bool
	w, h   int
}

func newModel(th theme.Theme, only string) model {
	keys := theme.DefaultKeymap()
	all := []pane{
		{s: presenceSurface(th, keys), seed: seedPresence},
		{s: streamSurface(th, keys), seed: seedStream},
		{s: artifactSurface(th, keys), seed: seedArtifact},
	}

	m := model{th: th, keys: keys}
	switch only {
	case "presence":
		m.panes, m.only = all[:1], true
	case "stream":
		m.panes, m.only = all[1:2], true
	case "artifact":
		m.panes, m.only = all[2:3], true
	default:
		m.panes = all
	}
	return m
}

// --- surface builders + seeds (mock data, nil client: the gallery never calls a
// fetch/publish path, only feeds the surfaces their own load/event messages) ---

func presenceSurface(th theme.Theme, keys theme.Keymap) surface.Surface {
	return surface.NewPresence(context.Background(), nil, th, keys)
}

func seedPresence(s surface.Surface) {
	s.Update(surface.ClientsLoadedMsg{Clients: []sextant.ClientInfo{
		{ID: "01HUMAN", DisplayName: "lena", Kind: theme.RoleHuman, Online: true},
		{ID: "01COORD", DisplayName: "coordinator-1", Kind: theme.RoleCoordinator, Online: true},
		{ID: "01DISP", DisplayName: "dispatcher-1", Kind: theme.RoleDispatcher, Online: false},
		{ID: "01ALPHA", DisplayName: "agent-alpha", Kind: theme.RoleAgent, Online: true},
		{ID: "01BETA", DisplayName: "agent-beta", Kind: theme.RoleAgent, Online: false},
		{ID: "01BUS", DisplayName: "bus", Kind: theme.RoleSystem, Online: true},
	}})
}

func streamSurface(th theme.Theme, keys theme.Keymap) surface.Surface {
	authors := map[string]surface.Author{
		"01HUMAN": {Name: "lena", Role: theme.RoleHuman},
		"01COORD": {Name: "coordinator-1", Role: theme.RoleCoordinator},
		"01ALPHA": {Name: "agent-alpha", Role: theme.RoleAgent},
	}
	return surface.NewStream(context.Background(), nil, "msg.topic.plan", th, keys,
		surface.WithCompose(), surface.WithAuthors(authors))
}

func seedStream(s surface.Surface) {
	lines := []struct{ author, text string }{
		{"01HUMAN", "let's get the dash building"},
		{"01COORD", "spinning up agent-alpha for the toolkit"},
		{"01ALPHA", "accepted — starting on theme + widgets"},
		{"01ALPHA", "palette resolved, goldens green"},
		{"01HUMAN", "nice — eyeball the gallery"},
		{"01COORD", "presence + stream + artifact all mount"},
	}
	for _, l := range lines {
		rec, _ := json.Marshal(map[string]string{"$type": "chat.message", "text": l.text})
		s.Update(busfeed.EventMsg{Message: sextant.Message{
			Frame:   wire.Frame{ID: "01" + l.author, Author: l.author, Kind: wire.KindMessage, Epoch: wire.Epoch, Record: rec},
			Subject: "msg.topic.plan",
		}})
	}
}

func artifactSurface(th theme.Theme, keys theme.Keymap) surface.Surface {
	return surface.NewArtifact(context.Background(), nil, "dash-plan", th, keys, surface.WithReview())
}

func seedArtifact(s surface.Surface) {
	body := "The dash assembles **pane-surfaces** into a layout the operator " +
		"controls, and ships a **cockpit** as the default assembly.\n\n" +
		"## The M4 panes\n\n" +
		"- **presence** — the clients directory\n" +
		"- **message stream** — one read-stream plus an optional compose\n" +
		"- **artifact** — a `document` reader, with a thin review affordance\n\n" +
		"Detail-on-demand is toggled, never an always-on column."
	rec, _ := json.Marshal(map[string]string{"$type": "document", "title": "Dash build plan", "body": body})
	s.Update(surface.ArtifactLoadedMsg{Artifact: sextant.Artifact{
		Name: "dash-plan", Record: wire.Lexicon(rec), Revision: 3,
	}})
}

// --- bubbletea ---

func (m model) Init() tea.Cmd {
	// Seed every surface up front; the gallery uses mock data, so no surface Init
	// (which would do live I/O) runs here.
	for _, p := range m.panes {
		p.seed(p.s)
	}
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		return m, nil
	case surface.DoneMsg:
		// A surface stepped out: return focus to the layout level.
		m.active = false
		m.applyFocus()
		return m, nil
	case surface.OpenMsg:
		// The dash would route this; the gallery has no detail pane, so it is a
		// no-op here (the intent is proven by the surface emitting it).
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		if !m.active {
			return m, tea.Quit
		}
	case "ctrl+c":
		return m, tea.Quit
	case "t":
		if !m.active {
			return m.toggleTheme(), nil
		}
	case "tab":
		if !m.active && !m.only {
			m.sel = (m.sel + 1) % len(m.panes)
			m.applyFocus()
			return m, nil
		}
	case "enter":
		if !m.active {
			m.active = true
			m.applyFocus()
			return m, nil
		}
	}
	if m.active {
		// Route the key into the focused surface; it may emit an intent (e.g.
		// DoneMsg on esc) which comes back through Update.
		return m, m.panes[m.sel].s.Update(msg)
	}
	return m, nil
}

func (m model) View() string {
	if m.w == 0 {
		return "starting surface gallery…"
	}
	p := m.panes[m.sel]
	focus := widget.FocusSelected
	if m.active {
		focus = widget.FocusActive
	}
	boxH := m.h - 1
	body := widget.Box(m.th, focus, p.s.Title(), m.titleHue(p.s.ID()), p.s.View(), m.w, boxH)
	return lipgloss.JoinVertical(lipgloss.Left, body, m.statusBar())
}

func (m model) statusBar() string {
	state := "selected"
	if m.active {
		state = "active"
	}
	left := lipgloss.NewStyle().Foreground(m.th.Fg).Render(
		fmt.Sprintf(" %s · %s ", m.panes[m.sel].s.ID(), state),
	)
	hints := "enter step in · esc out · ↑/↓ move · t theme · q quit "
	if !m.only {
		hints = "tab cycle · " + hints
	}
	hint := lipgloss.NewStyle().Foreground(m.th.Dim).Render(hints)
	gap := m.w - lipgloss.Width(left) - lipgloss.Width(hint)
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + hint
	return lipgloss.NewStyle().Background(m.th.Panel).Width(m.w).MaxWidth(m.w).Render(bar)
}

// titleHue tints the pane chrome by what the pane is, the same convention the
// widget gallery uses.
func (m model) titleHue(id string) lipgloss.Color {
	switch id {
	case "presence":
		return m.th.RoleHue(theme.RoleHuman)
	case "artifact":
		return m.th.KindHue(theme.KindArtifactUpdate)
	default:
		return m.th.KindHue(theme.KindChat)
	}
}

// applyFocus sets each surface's focus from the gallery's selection + active
// state, so only the shown-and-active surface lights its inner cursor.
func (m *model) applyFocus() {
	for i, p := range m.panes {
		f := widget.FocusIdle
		if i == m.sel {
			f = widget.FocusSelected
			if m.active {
				f = widget.FocusActive
			}
		}
		p.s.SetFocus(f)
	}
}

func (m model) toggleTheme() model {
	if m.th.Variant == theme.VariantLight {
		m.th = theme.Dark()
	} else {
		m.th = theme.Light()
	}
	// Rebuild surfaces against the new theme (hues are resolved at construction),
	// re-seed, and restore size + focus.
	nm := newModel(m.th, m.onlyName())
	nm.sel, nm.active, nm.w, nm.h = m.sel, m.active, m.w, m.h
	for _, p := range nm.panes {
		p.seed(p.s)
	}
	nm.layout()
	nm.applyFocus()
	return nm
}

func (m model) onlyName() string {
	if !m.only {
		return ""
	}
	return m.panes[0].s.ID()
}

// layout sizes the shown surface to the box's inner content area, the same
// w-4/h-2 convention the dash's layout engine uses.
func (m *model) layout() {
	if m.w == 0 {
		return
	}
	boxH := m.h - 1
	innerW, innerH := m.w-4, boxH-2
	if innerW < 1 {
		innerW = 1
	}
	if innerH < 1 {
		innerH = 1
	}
	for _, p := range m.panes {
		p.s.SetSize(innerW, innerH)
	}
	m.applyFocus()
}
