package surface

import (
	"context"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// clientsRefreshInterval is how often the clients browser re-fetches the
// directory. Presence is connection-derived with no watch API (ADR-0020), so the
// browser polls: an initial fetch in Init, then a tick. It matches the standalone
// Presence surface's interval — the data source is the same (clients.list).
const clientsRefreshInterval = presenceRefreshInterval

// ClientsBrowser is the clients browser (ADR-0024): a list of every issued
// identity that opens a direct conversation (a DM) with the selected client in
// place. It is the presence directory turned into a Browser — same data source
// (client.ListClients, polled), same rendering (a display name in its role hue,
// led by a status glyph carrying liveness by shape, via clientRow) — whose Enter
// opens a Stream on the client's direct subject (msg.client.<id>) rather than
// emitting an open intent. A DM and a topic room are the same conversation
// surface over different subjects (ADR-0024), so the detail is the same Stream
// the topics browser opens.
//
// It embeds Browser and supplies the data: Init fetches the directory and arms a
// refresh tick; each ClientsLoadedMsg rebuilds the rows (via setRows) and the
// parallel last slice so Enter resolves the selected client by cursor index
// (display names are unique only by convention, so never reverse-map the label).
type ClientsBrowser struct {
	Browser

	client *sextant.Client
	ctx    context.Context

	// last is the most recent snapshot, sorted in row order, so openRow resolves
	// the selected client by cursor index (and a re-theme could rebuild from it).
	last []sextant.ClientInfo
	// err holds the most recent fetch failure for the footer (fail-loud); the next
	// successful refresh clears it. The last good snapshot stays either way.
	err error
	// stopped gates the fetch and tick so teardown ends the refresh loop cleanly.
	stopped bool
}

// NewClientsBrowser builds a clients browser over client. Pass a context that
// lives as long as the browser (it scopes the directory fetches) and the resolved
// theme/keymap. The browser does no I/O until Init.
func NewClientsBrowser(ctx context.Context, client *sextant.Client, th theme.Theme, keys theme.Keymap) *ClientsBrowser {
	c := &ClientsBrowser{client: client, ctx: ctx}
	c.Browser = newBrowser("clients", "Clients", keys, th, func(cursor int) (Surface, string) {
		if cursor < 0 || cursor >= len(c.last) {
			return nil, ""
		}
		ci := c.last[cursor]
		// Enter opens a direct conversation on the client's direct subject. A DM is
		// the same conversation surface as a topic, over a different subject; the
		// title names the mode, matching "Topic · x" / "Artifact · x".
		s := NewStream(c.ctx, c.client, sx.ClientSubject(ci.ID), c.th, c.keys, WithCompose())
		return s, "DM · " + ci.DisplayName
	})
	return c
}

// Init fetches the directory once and arms the refresh tick. The first
// ClientsLoadedMsg populates the list; the tick re-fetches on the interval.
func (c *ClientsBrowser) Init() tea.Cmd {
	return tea.Batch(c.fetch(), c.tick())
}

// Update folds in the directory snapshots, the refresh tick, and the fetch error,
// then delegates to Browser.Update for navigation and detail delegation.
func (c *ClientsBrowser) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ClientsLoadedMsg:
		c.applySnapshot(msg.Clients)
		return nil
	case clientsErrMsg:
		// Surface the failure in the footer (fail-loud); the last good snapshot
		// stays visible, and the next successful refresh clears the footer.
		c.err = msg.err
		return nil
	case clientsTickMsg:
		// Re-fetch and re-arm. Both run even before the first snapshot, so a slow
		// first fetch does not stall the refresh loop.
		return tea.Batch(c.fetch(), c.tick())
	}
	return c.Browser.Update(msg)
}

// View renders the list (or the open detail) with a fetch-error footer below it
// when one is showing — kept visible rather than swallowed (fail-loud). At the
// detail level the inner surface owns its own footer, so the fetch error only
// shows at the list.
func (c *ClientsBrowser) View() string {
	body := c.Browser.View()
	if c.err != nil && !c.inDetail() {
		return body + "\n" + errorFooter(c.th, c.err, c.w)
	}
	return body
}

// Stop ends the refresh loop (fetch and tick no-op after it) and tears down any
// open detail. The clients browser owns no feed or watch of its own — only the
// poll loop — so beyond stopping the loop it just delegates to stopDetail.
func (c *ClientsBrowser) Stop() {
	c.stopped = true
	c.stopDetail()
}

// applySnapshot stores a directory snapshot (sorted into row order, so openRow
// resolves the selected client by cursor index) and rebuilds the list rows from
// it. Rows are sorted by display name then id, the same order the standalone
// Presence surface uses, so the two read identically. A successful snapshot
// clears any fetch-error footer (the bus is reachable again).
func (c *ClientsBrowser) applySnapshot(clients []sextant.ClientInfo) {
	sorted := make([]sextant.ClientInfo, len(clients))
	copy(sorted, clients)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].DisplayName != sorted[j].DisplayName {
			return sorted[i].DisplayName < sorted[j].DisplayName
		}
		return sorted[i].ID < sorted[j].ID
	})

	items := make([]widget.ListItem, len(sorted))
	for i, ci := range sorted {
		items[i] = clientRow(c.th, ci)
	}
	c.last = sorted
	c.err = nil
	c.setRows(items)
}

// fetch reads the directory off the main loop and returns it as a ClientsLoadedMsg
// (or a clientsErrMsg). It is bounded by a short deadline so a wedged bus surfaces
// an error instead of hanging the refresh. After Stop it is a no-op. A nil client
// (a seeded gallery / golden) skips the fetch — those feed ClientsLoadedMsg
// directly.
func (c *ClientsBrowser) fetch() tea.Cmd {
	if c.stopped || c.client == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
		defer cancel()
		infos, err := c.client.ListClients(ctx)
		if err != nil {
			return clientsErrMsg{err: err}
		}
		return ClientsLoadedMsg{Clients: infos}
	}
}

// tick schedules the next refresh, or nothing after Stop (ending the loop).
func (c *ClientsBrowser) tick() tea.Cmd {
	if c.stopped {
		return nil
	}
	return tea.Tick(clientsRefreshInterval, func(time.Time) tea.Msg { return clientsTickMsg{} })
}

// clientsTickMsg fires on the refresh interval to trigger the next fetch. It is
// distinct from the standalone Presence surface's presenceTickMsg so the two
// refresh loops never cross when both are mounted (R3 retires Presence).
type clientsTickMsg struct{}

// clientRow maps one ClientInfo to a list row: the display name in its role hue,
// led by a status glyph whose shape and hue carry liveness. Offline is the hollow
// ○ in the dim line colour; online is the filled ● in the connected hue. It is the
// shared row-builder both the standalone Presence surface and the clients browser
// render through, so the two read identically (ADR-0024 folds presence into the
// browser).
func clientRow(th theme.Theme, ci sextant.ClientInfo) widget.ListItem {
	glyph := theme.StatusGlyph("") // hollow ○ for offline
	glyphHue := th.Dim
	if ci.Online {
		glyph = theme.StatusGlyph(theme.StatusConnected) // filled ●
		glyphHue = th.StatusHue(theme.StatusConnected)
	}
	return widget.ListItem{
		Title:    ci.DisplayName,
		Glyph:    glyph,
		Hue:      th.RoleHue(ci.Kind),
		GlyphHue: glyphHue,
	}
}
