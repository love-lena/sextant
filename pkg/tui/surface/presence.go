package surface

import (
	"context"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
// last good snapshot and renders the error in its footer; a transient failure
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

	// ids holds the client id for each list row, in the same sorted order as the
	// rows. Selection resolves through it by cursor index, so two clients sharing
	// a display name (unique only by convention, clients.go) never collide.
	ids []string
	// last is the most recent snapshot, kept so SetTheme can rebuild the rows with
	// re-resolved hues without waiting for the next poll (the row hues are baked in
	// at snapshot time, so a runtime theme switch must re-apply the snapshot).
	last []sextant.ClientInfo
	// loaded is false until the first snapshot arrives, so View distinguishes
	// "still loading" from "genuinely empty".
	loaded bool
	// stopped is set by Stop; fetch and the refresh tick no-op once it is true, so
	// teardown ends the refresh loop cleanly.
	stopped bool
	// w, h is the inner area; the error footer, when present, takes the last row.
	w, h int
	err  error
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

// Title returns the pane label: the surface type, so the chrome names what the
// pane is rather than carrying a bare id.
func (p *Presence) Title() string { return "Presence" }

// SetSize sizes the inner list area, reserving the bottom row for the error
// footer when an error is present.
func (p *Presence) SetSize(w, h int) {
	p.w, p.h = w, h
	p.relayout()
}

// relayout sizes the list to the inner area minus a row for the error footer
// (only when an error is showing), so the footer never overlaps the last row.
func (p *Presence) relayout() {
	listH := p.h
	if p.err != nil {
		listH = p.h - 1
		if listH < 1 {
			listH = 1
		}
	}
	p.list.SetSize(p.w, listH)
}

// SetTheme re-themes the surface: it stores the new theme and rebuilds the list
// rows from the last snapshot. The rows are list items carrying baked-in role and
// status hues (resolved at snapshot time), so a runtime theme switch must
// re-apply the snapshot to re-resolve those hues — the next render then shows the
// new palette.
func (p *Presence) SetTheme(th theme.Theme) {
	p.theme = th
	if p.loaded {
		p.applySnapshot(p.last)
	}
}

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
		// Surface the error in the footer; keep the last good snapshot visible.
		p.err = msg.err
		p.relayout()
		return nil
	case presenceTickMsg:
		// Re-fetch and re-arm the tick. Both run even before the first snapshot,
		// so a slow first fetch does not stall the refresh loop.
		return tea.Batch(p.fetch(), p.tick())
	case tea.KeyMsg:
		if p.focus != widget.FocusActive {
			return nil
		}
		// Bindings come from the keymap (keys are data), not literal strings, so a
		// rebind is honoured here as it is in the chrome and the list widget.
		switch {
		case key.Matches(msg, p.keys.Back):
			// Step out: hand focus back to the layout level (uniform across surfaces).
			return doneCmd(p.ID())
		case key.Matches(msg, p.keys.Enter):
			// Open a direct view of the selected client, resolved by cursor index
			// (display names are not unique, so never reverse-map the label).
			if id := p.selectedID(); id != "" {
				return openCmd(OpenClient, id)
			}
			return nil
		}
		// The nav bindings move the cursor (the list reads them from the keymap too).
		p.list, _ = p.list.Update(msg)
		return nil
	}
	return nil
}

// View renders the directory list (inner content only; the layout draws chrome).
// Before the first snapshot it shows a "loading…" placeholder so an empty pane
// is never mistaken for "no clients"; an error is shown in a footer line, kept
// honest rather than swallowed.
func (p *Presence) View() string {
	var body string
	if !p.loaded {
		w := p.w
		if w <= 0 {
			w = 1
		}
		body = lipgloss.NewStyle().Foreground(p.theme.Dim).Width(w).Render("loading…")
	} else {
		body = p.list.View(p.theme, p.focus)
	}
	if p.err != nil {
		return lipgloss.JoinVertical(lipgloss.Left, body, errorFooter(p.theme, p.err, p.w))
	}
	return body
}

// Stop ends the refresh loop: fetch and the tick no-op after it, so no further
// directory reads are issued. Presence holds no goroutine of its own (the tick
// is a one-shot timer scoped to the surface ctx), so this is the uniform-contract
// teardown rather than a goroutine join. It is safe to call more than once.
func (p *Presence) Stop() { p.stopped = true }

// fetch is the tea.Cmd that reads the directory off the main loop and returns it
// as a ClientsLoadedMsg (or a clientsErrMsg). It is bounded by a short deadline
// so a wedged bus surfaces an error instead of hanging the refresh. After Stop it
// is a no-op.
func (p *Presence) fetch() tea.Cmd {
	if p.stopped {
		return nil
	}
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

// tick schedules the next refresh, or nothing after Stop (ending the loop).
func (p *Presence) tick() tea.Cmd {
	if p.stopped {
		return nil
	}
	return tea.Tick(presenceRefreshInterval, func(time.Time) tea.Msg { return presenceTickMsg{} })
}

// applySnapshot stores a directory snapshot and rebuilds the list rows from it,
// keeping the cursor where it was by index. It records each row's client id in a
// parallel slice so selection resolves by index, and clears any prior error
// footer (a successful fetch means the bus is reachable again).
func (p *Presence) applySnapshot(clients []sextant.ClientInfo) {
	p.loaded = true
	p.last = clients
	hadErr := p.err != nil
	p.err = nil
	sorted := make([]sextant.ClientInfo, len(clients))
	copy(sorted, clients)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].DisplayName != sorted[j].DisplayName {
			return sorted[i].DisplayName < sorted[j].DisplayName
		}
		return sorted[i].ID < sorted[j].ID
	})

	cursor := p.list.Cursor()
	items := make([]widget.ListItem, len(sorted))
	p.ids = make([]string, len(sorted))
	for i, ci := range sorted {
		items[i] = clientRow(p.theme, ci)
		p.ids[i] = ci.ID
	}
	p.list.SetItems(items)
	if cursor >= 0 {
		p.list.SetCursor(cursor)
	}
	if hadErr {
		// The footer just went away; give its row back to the list.
		p.relayout()
	}
}

// selectedID returns the client id at the list cursor, or "" when the list is
// empty or the index is out of range. Selection is by index into the parallel
// ids slice, never by reverse-mapping the display label (names are not unique).
func (p *Presence) selectedID() string {
	i := p.list.Cursor()
	if i < 0 || i >= len(p.ids) {
		return ""
	}
	return p.ids[i]
}
