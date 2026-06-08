package surface

import (
	"context"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// presenceRefreshInterval is how often the presence surface re-fetches the
// clients directory. Presence is connection-derived with no watch API
// (ADR-0020), so the surface polls: an initial fetch in Init, then a tick.
const presenceRefreshInterval = 2 * time.Second

// ClientsLoadedMsg carries a directory snapshot to the presence surface. It is
// the result of the surface's own ListClients fetch and the seam a test (or a
// seeded gallery) feeds synthetic clients through — the surface renders entirely
// from the most recent snapshot, never from a live call inside View.
type ClientsLoadedMsg struct {
	// Clients is the directory snapshot, as returned by client.ListClients.
	Clients []sextant.ClientInfo
}

// clientsErrMsg reports that a directory fetch failed. The surface keeps the
// last good snapshot and records the error for its footer; a transient failure
// does not blank the pane.
type clientsErrMsg struct {
	err error
}

// presenceTickMsg fires on the refresh interval to trigger the next fetch.
type presenceTickMsg struct{}

// Presence renders the clients directory (ADR-0023): one row per issued
// identity, the display name in its role hue, a status glyph carrying liveness
// by shape. It is built on the cursor List widget and refreshes by polling,
// since presence is connection-derived with no watch.
//
// Selecting a row emits an OpenClient intent (open a direct view of that client)
// — kept lightweight, since M4 is manual comms. Presence renders only from the
// last ClientsLoadedMsg, so a test feeds it synthetic clients without a bus.
type Presence struct {
	client *sextant.Client
	ctx    context.Context
	theme  theme.Theme
	keys   theme.Keymap

	list  widget.List
	focus widget.Focus

	// clients is the last good snapshot, sorted by display name for a stable order.
	clients []sextant.ClientInfo
	// loaded is false until the first snapshot arrives, so View can distinguish
	// "still loading" from "genuinely empty".
	loaded bool
	err    error
}

// NewPresence builds a presence surface over client. Pass a context that lives as
// long as the surface (it scopes the directory fetches) and the theme/keymap the
// dash resolved. The surface does no I/O until Init.
func NewPresence(ctx context.Context, client *sextant.Client, th theme.Theme, keys theme.Keymap) *Presence {
	return &Presence{
		client: client,
		ctx:    ctx,
		theme:  th,
		keys:   keys,
		list:   widget.NewList(keys),
	}
}

// ID returns the stable layout id.
func (p *Presence) ID() string { return "presence" }

// Title returns the pane label.
func (p *Presence) Title() string { return "presence" }

// SetSize sizes the inner list area.
func (p *Presence) SetSize(w, h int) { p.list.SetSize(w, h) }

// SetFocus sets the three-state focus; the cursor bar lights only when active.
func (p *Presence) SetFocus(f widget.Focus) { p.focus = f }

// Init fetches the directory once and arms the refresh tick. The first
// ClientsLoadedMsg populates the list; the tick re-fetches on the interval.
func (p *Presence) Init() tea.Cmd {
	return tea.Batch(p.fetch(), p.tick())
}

// Update handles directory snapshots, the refresh tick, and — while active —
// cursor movement and the select-to-open intent.
func (p *Presence) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ClientsLoadedMsg:
		p.applySnapshot(msg.Clients)
		return nil
	case clientsErrMsg:
		p.err = msg.err
		return nil
	case presenceTickMsg:
		// Re-fetch and re-arm the tick. Both run even before the first snapshot,
		// so a slow first fetch does not stall the refresh loop.
		return tea.Batch(p.fetch(), p.tick())
	case tea.KeyMsg:
		if p.focus != widget.FocusActive {
			return nil
		}
		switch msg.String() {
		case "esc":
			// Step out: hand focus back to the layout level (uniform across surfaces).
			return doneCmd(p.ID())
		case "enter":
			// Open a direct view of the selected client.
			if it, ok := p.list.Selected(); ok && it.Title != "" {
				if id := p.idFor(it.Title); id != "" {
					return openCmd(OpenClient, id)
				}
			}
			return nil
		}
		// Arrows move the cursor.
		p.list, _ = p.list.Update(msg)
		return nil
	}
	return nil
}

// View renders the directory list (inner content only; the layout draws chrome).
func (p *Presence) View() string {
	return p.list.View(p.theme, p.focus)
}

// fetch is the tea.Cmd that reads the directory off the main loop and returns it
// as a ClientsLoadedMsg (or a clientsErrMsg). It is bounded by a short deadline
// so a wedged bus surfaces an error instead of hanging the refresh.
func (p *Presence) fetch() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
		defer cancel()
		infos, err := p.client.ListClients(ctx)
		if err != nil {
			return clientsErrMsg{err: err}
		}
		return ClientsLoadedMsg{Clients: infos}
	}
}

// tick schedules the next refresh.
func (p *Presence) tick() tea.Cmd {
	return tea.Tick(presenceRefreshInterval, func(time.Time) tea.Msg { return presenceTickMsg{} })
}

// applySnapshot stores a directory snapshot and rebuilds the list rows from it,
// keeping the cursor where it was by index.
func (p *Presence) applySnapshot(clients []sextant.ClientInfo) {
	p.loaded = true
	p.err = nil
	sorted := make([]sextant.ClientInfo, len(clients))
	copy(sorted, clients)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].DisplayName != sorted[j].DisplayName {
			return sorted[i].DisplayName < sorted[j].DisplayName
		}
		return sorted[i].ID < sorted[j].ID
	})
	p.clients = sorted

	cursor := p.list.Cursor()
	items := make([]widget.ListItem, len(sorted))
	for i, ci := range sorted {
		items[i] = p.rowFor(ci)
	}
	p.list.SetItems(items)
	if cursor >= 0 {
		p.list.SetCursor(cursor)
	}
}

// rowFor maps one ClientInfo to a list row: the display name in its role hue,
// led by a status glyph whose shape and hue carry liveness. Offline is the
// hollow ○ in the dim line colour; online is the filled ● in the connected hue.
func (p *Presence) rowFor(ci sextant.ClientInfo) widget.ListItem {
	glyph := theme.StatusGlyph("") // hollow ○ for offline
	glyphHue := p.theme.Dim
	if ci.Online {
		glyph = theme.StatusGlyph(theme.StatusConnected) // filled ●
		glyphHue = p.theme.StatusHue(theme.StatusConnected)
	}
	return widget.ListItem{
		Title:    ci.DisplayName,
		Glyph:    glyph,
		Hue:      p.theme.RoleHue(ci.Kind),
		GlyphHue: glyphHue,
	}
}

// idFor returns the client id whose display name matches name, or "" if none.
// The list holds display names (the human label); the intent carries the id.
func (p *Presence) idFor(name string) string {
	for _, ci := range p.clients {
		if ci.DisplayName == name {
			return ci.ID
		}
	}
	return ""
}
