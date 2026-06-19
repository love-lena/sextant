// Command dash-surfacegallery is a preview binary for the dash's pane-surfaces.
// It runs each surface — the three ADR-0024 browsers (clients, topics,
// artifacts) plus the two detail surfaces they open (the message stream and the
// artifact reader/review) — standalone, against SEEDED mock data so the demo is
// deterministic and needs no bus. It proves the Surface contract works in
// isolation (a surface is its own tea.Program) AND that the same surface type
// mounts as a pane unchanged: the host wraps the surface's inner View in the
// shared widget.Box, exactly as the dash's layout will.
//
// Run: go run ./cmd/dash-surfacegallery
//
//	[--surface clients|topics|artifacts|stream|artifact]
//	[--theme light|dark|auto]
//
// The shown surface is always focused (ADR-0026: keys go to the focused
// surface; there is no step-in level). Keys: tab / shift+tab cycle the
// surfaces · enter opens the selected row's detail IN PLACE (in a browser) or
// sends (in a compose) · esc pops a detail back to its list · ↑/↓ move within
// the surface · (stream/artifact) type to compose · t toggles theme and q
// quits while the surface is not capturing text (while composing they type) ·
// ctrl+c always quits.
//
// A browser's opened detail runs with a nil client, so its conversation/reader
// starts empty (no live feed) — the master⇄detail motion is what this gallery
// shows; the seeded standalone stream/artifact entries show the detail surfaces
// with content.
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
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/clients/go/apps/internal/tui/busfeed"
	"github.com/love-lena/sextant/clients/go/apps/internal/tui/surface"
	"github.com/love-lena/sextant/clients/go/apps/internal/tui/theme"
	"github.com/love-lena/sextant/clients/go/apps/internal/tui/widget"
	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/wire"
)

func main() {
	surfaceFlag := flag.String("surface", "", "show only one surface: clients, topics, artifacts, stream, or artifact (default: all, tab to cycle)")
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

// model hosts the surfaces and tracks which one is shown. The shown surface is
// always focused (ADR-0026) — keys go straight to it; the host claims only the
// cycle keys, the theme toggle, and quitting. It demonstrates that a surface
// mounts unchanged inside widget.Box.
type model struct {
	th    theme.Theme
	keys  theme.Keymap
	panes []pane
	only  bool // a single surface (no cycling)

	sel  int
	w, h int
}

// galleryOrder is the --surface names in pane order.
var galleryOrder = []string{"clients", "topics", "artifacts", "stream", "artifact"}

func newModel(th theme.Theme, only string) model {
	keys := theme.DefaultKeymap()
	all := []pane{
		{s: clientsBrowser(th, keys), seed: seedClients},
		{s: topicsBrowser(th, keys), seed: seedTopics},
		{s: artifactsBrowser(th, keys), seed: seedArtifacts},
		{s: streamSurface(th, keys), seed: seedStream},
		{s: artifactSurface(th, keys), seed: seedArtifact},
	}

	m := model{th: th, keys: keys}
	m.panes = all
	for i, name := range galleryOrder {
		if only == name {
			m.panes, m.only = all[i:i+1], true
			break
		}
	}
	return m
}

// --- surface builders + seeds (mock data, nil client: the gallery never calls a
// live fetch/publish path, only feeds the surfaces their own load/event messages) ---

func clientsBrowser(th theme.Theme, keys theme.Keymap) surface.Surface {
	return surface.NewClientsBrowser(context.Background(), nil, th, keys)
}

func seedClients(s surface.Surface) {
	s.Update(surface.ClientsLoadedMsg{Clients: []sextant.ClientInfo{
		{ID: "01HUMAN", DisplayName: "lena", Kind: theme.RoleHuman, Online: true},
		{ID: "01COORD", DisplayName: "coordinator-1", Kind: theme.RoleCoordinator, Online: true},
		{ID: "01DISP", DisplayName: "dispatcher-1", Kind: theme.RoleDispatcher, Online: false},
		{ID: "01ALPHA", DisplayName: "agent-alpha", Kind: theme.RoleAgent, Online: true},
		{ID: "01BETA", DisplayName: "agent-beta", Kind: theme.RoleAgent, Online: false},
		{ID: "01BUS", DisplayName: "bus", Kind: theme.RoleSystem, Online: true},
	}})
}

func topicsBrowser(th theme.Theme, keys theme.Keymap) surface.Surface {
	return surface.NewTopicsBrowser(context.Background(), nil, th, keys)
}

// seedTopics teaches the topics browser its names the way the live discovery
// feed would: one replayed frame per topic, the name carried by the subject.
func seedTopics(s surface.Surface) {
	for _, topic := range []string{"plan", "build", "review"} {
		rec, _ := json.Marshal(map[string]string{"$type": "chat.message", "text": "hello " + topic})
		s.Update(busfeed.EventMsg{Message: sextant.Message{
			Frame:   wire.Frame{ID: "01" + topic, Author: "01HUMAN", Kind: wire.KindMessage, Epoch: wire.Epoch, Record: rec},
			Subject: "msg.topic." + topic,
		}})
	}
}

func artifactsBrowser(th theme.Theme, keys theme.Keymap) surface.Surface {
	return surface.NewArtifactsBrowser(context.Background(), nil, th, keys)
}

func seedArtifacts(s surface.Surface) {
	s.Update(surface.ArtifactsLoadedMsg{Artifacts: []sextant.ArtifactInfo{
		{Name: "dash-plan", Revision: 3, Updated: time.Unix(0, 0)},
		{Name: "protocol-notes", Revision: 12, Updated: time.Unix(0, 0)},
		{Name: "scratch", Revision: 0, Updated: time.Unix(0, 0)},
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
		{"01COORD", "the three browsers all mount"},
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
	body := "The dash is a cockpit of three **browsers** — `clients`, `topics`, and " +
		"`artifacts` — each a list of what exists that you step into.\n\n" +
		"## The three browsers\n\n" +
		"- **clients** — the directory; Enter opens a direct conversation\n" +
		"- **topics** — every topic with messages; Enter opens its conversation\n" +
		"- **artifacts** — the bucket's documents; Enter opens the live reader\n\n" +
		"A detail opens inside the same pane; Esc pops back to the list."
	rec, _ := json.Marshal(map[string]string{"$type": "document", "title": "The three browsers", "body": body})
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
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleKey claims the host keys (cycle, theme, quit) and delivers everything
// else to the shown surface (ADR-0026: keys go to the focused surface). The
// cycle keys and ctrl+c are never claimed by a surface, so they work
// mid-compose; the printable host keys (q, t) act only while the surface is not
// capturing text — while a compose is capturing they type.
func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.stopAll()
		return m, tea.Quit
	case "tab", "shift+tab":
		if !m.only {
			step := 1
			if msg.String() == "shift+tab" {
				step = len(m.panes) - 1
			}
			m.sel = (m.sel + step) % len(m.panes)
			m.applyFocus()
		}
		return m, nil
	}
	if !m.panes[m.sel].s.CapturingText() {
		switch msg.String() {
		case "q":
			m.stopAll()
			return m, tea.Quit
		case "t":
			return m.toggleTheme(), nil
		}
	}
	return m, m.panes[m.sel].s.Update(msg)
}

// stopAll tears down every surface on quit (the Surface contract's teardown), so
// a feed-backed surface releases its pump cleanly.
func (m model) stopAll() {
	for _, p := range m.panes {
		p.s.Stop()
	}
}

func (m model) View() string {
	if m.w == 0 {
		return "starting surface gallery…"
	}
	p := m.panes[m.sel]
	boxH := m.h - 1
	body := widget.Box(m.th, widget.FocusActive, p.s.Title(), m.titleHue(p.s.ID()), p.s.View(), m.w, boxH)
	return lipgloss.JoinVertical(lipgloss.Left, body, m.statusBar())
}

func (m model) statusBar() string {
	left := lipgloss.NewStyle().Foreground(m.th.Fg).Render(
		fmt.Sprintf(" %s — %s ", m.panes[m.sel].s.ID(), m.panes[m.sel].s.Title()),
	)
	hints := "enter open · esc back · ↑/↓ move · t theme · q quit "
	if !m.only {
		hints = "tab next · " + hints
	}
	hint := lipgloss.NewStyle().Foreground(m.th.Dim).Render(hints)
	gap := m.w - lipgloss.Width(left) - lipgloss.Width(hint)
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + hint
	return lipgloss.NewStyle().Background(m.th.Panel).Width(m.w).MaxWidth(m.w).Render(bar)
}

// titleHue tints the pane chrome by what the pane is — keyed off the STABLE id,
// the same convention the dash's layout uses, so a browser's frame keeps its hue
// while its title tracks an open detail.
func (m model) titleHue(id string) lipgloss.Color {
	switch id {
	case "clients":
		return m.th.RoleHue(theme.RoleHuman)
	case "artifacts", "artifact":
		return m.th.KindHue(theme.KindArtifactUpdate)
	default:
		return m.th.KindHue(theme.KindChat)
	}
}

// applyFocus sets each surface's focus: the shown surface is active (it is the
// focused pane — its cursor and compose light up), the rest idle.
func (m *model) applyFocus() {
	for i, p := range m.panes {
		f := widget.FocusIdle
		if i == m.sel {
			f = widget.FocusActive
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
	nm.sel, nm.w, nm.h = m.sel, m.w, m.h
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
