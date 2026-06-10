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
// browser polls: an initial fetch in Init, then a tick.
const clientsRefreshInterval = 2 * time.Second

// ClientsLoadedMsg carries a clients-directory snapshot to the clients browser.
// It is the result of the browser's own ListClients fetch and the seam a test
// (or a seeded gallery) feeds synthetic clients through — the browser renders
// entirely from the most recent snapshot, never from a live call inside View.
type ClientsLoadedMsg struct {
	// Clients is the directory snapshot, as returned by client.ListClients.
	Clients []sextant.ClientInfo
}

// clientsErrMsg reports that a directory fetch failed. The browser keeps the
// last good snapshot (the list does not blank) and a successful refresh
// recovers.
type clientsErrMsg struct {
	err error
}

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
		// title names the mode, matching "Topic · x" / "Artifact · x". The browser
		// already holds the directory, so it resolves the conversation's authors
		// from its own latest snapshot — names in role hues, not raw ids.
		s := NewStream(c.ctx, c.client, sx.ClientSubject(ci.ID), c.th, c.keys,
			WithCompose(), WithAuthors(c.authors()))
		return s, "DM · " + ci.DisplayName
	})
	return c
}

// authors maps the latest directory snapshot to the id → Author resolution an
// opened conversation renders with (display name + role hue).
func (c *ClientsBrowser) authors() map[string]Author {
	out := make(map[string]Author, len(c.last))
	for _, ci := range c.last {
		out[ci.ID] = Author{Name: ci.DisplayName, Role: ci.Kind}
	}
	return out
}

// Init fetches the directory once and arms the refresh tick. The first
// ClientsLoadedMsg populates the list; the tick re-fetches on the interval.
func (c *ClientsBrowser) Init() tea.Cmd {
	return tea.Batch(c.fetch(), c.tick())
}

// SetSize sizes the inner area. It reserves the bottom row for the error footer
// when one is showing (so a full list never clips it) and sizes the detail if
// one is open.
func (c *ClientsBrowser) SetSize(w, h int) {
	c.Browser.SetSize(w, h)
	c.relayoutList(c.err != nil)
}

// SetTheme re-themes the browser: the list rows bake in role/status hues at
// snapshot time, so a runtime theme switch re-applies the last snapshot to
// re-resolve them (the embedded Browser re-themes itself and any open detail).
func (c *ClientsBrowser) SetTheme(th theme.Theme) {
	c.Browser.SetTheme(th)
	err := c.err // re-applying the snapshot is not a successful fetch
	c.applySnapshot(c.last)
	c.err = err
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
		c.relayoutList(c.err != nil)
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
// it. Rows are sorted by display name then id. A successful snapshot clears any
// fetch-error footer (the bus is reachable again).
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
	keys := make([]string, len(sorted))
	for i, ci := range sorted {
		items[i] = clientRow(c.th, ci)
		keys[i] = ci.ID // the stable identity the selection survives re-sorts by
	}
	c.last = sorted
	c.err = nil
	c.relayoutList(false) // error cleared: restore full list height
	c.setRows(items, keys)
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

// clientsTickMsg fires on the refresh interval to trigger the next fetch.
type clientsTickMsg struct{}

// clientRow maps one ClientInfo to a list row: the display name in its role hue,
// led by a status glyph whose shape and hue carry liveness. Offline is the hollow
// ○ in the dim line colour; online is the filled ● in the connected hue (ADR-0024
// folds the old presence directory into this browser).
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
