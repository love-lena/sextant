package surface

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// artifactsRefreshInterval is how often the artifacts browser re-fetches the
// directory. There is no list-watch (ADR-0024: artifact.list is a read verb, not
// a watch), so the browser polls: an initial fetch in Init, then a tick. The
// document reader it opens is itself live (artifact.watch), so only the directory
// is polled, not the contents.
const artifactsRefreshInterval = 2 * time.Second

// ArtifactsLoadedMsg carries an artifacts directory snapshot to the artifacts
// browser. It is the result of the browser's own ListArtifacts fetch and the seam
// a test (or a seeded gallery) feeds synthetic artifacts through — the browser
// builds its rows entirely from the most recent snapshot.
type ArtifactsLoadedMsg struct {
	// Artifacts is the directory snapshot, as returned by client.ListArtifacts.
	Artifacts []sextant.ArtifactInfo
}

// artifactsErrMsg reports that a directory fetch failed. The browser keeps the
// last good snapshot (the list does not blank) and a successful refresh recovers.
type artifactsErrMsg struct {
	err error
}

// artifactsTickMsg fires on the refresh interval to trigger the next fetch.
type artifactsTickMsg struct{}

// ArtifactsBrowser is the artifacts browser (ADR-0024): a list of every artifact
// the ARTIFACTS bucket holds that opens the document reader for the selected one
// in place. The list comes from the artifact.list read verb (ListArtifacts) —
// discovery of existing state, not a new construct — polled on a refresh tick
// since there is no list-watch. Each row is the artifact name plus a dim "rev N"
// cue; Enter opens an Artifact reader, kept live by its own artifact.watch.
//
// It embeds Browser and supplies the data: Init fetches the directory and arms a
// refresh tick; each ArtifactsLoadedMsg rebuilds the rows (via setRows) and the
// parallel names slice so Enter resolves the selected artifact by cursor index.
type ArtifactsBrowser struct {
	Browser

	client *sextant.Client
	ctx    context.Context

	// names holds the artifact name for each list row, in the same sorted order as
	// the rows, so openRow resolves the selected artifact by cursor index.
	names []string
	// last is the most recent snapshot, kept so SetTheme can rebuild the rows with
	// re-resolved hues without waiting for the next poll.
	last []sextant.ArtifactInfo
	// err holds the most recent fetch failure for the footer (fail-loud); the next
	// successful refresh clears it. The last good snapshot stays either way.
	err error
	// stopped gates the fetch and tick so teardown ends the refresh loop cleanly.
	stopped bool
}

// NewArtifactsBrowser builds an artifacts browser over client. Pass a context
// that lives as long as the browser (it scopes the directory fetches and each
// opened reader's watch) and the resolved theme/keymap. The browser does no I/O
// until Init.
func NewArtifactsBrowser(ctx context.Context, client *sextant.Client, th theme.Theme, keys theme.Keymap) *ArtifactsBrowser {
	a := &ArtifactsBrowser{client: client, ctx: ctx}
	a.Browser = newBrowser("artifacts", "Artifacts", keys, th, func(cursor int) (Surface, string) {
		if cursor < 0 || cursor >= len(a.names) {
			return nil, ""
		}
		name := a.names[cursor]
		// Enter opens the document reader (kept live by its own artifact.watch).
		s := NewArtifact(a.ctx, a.client, name, a.th, a.keys)
		return s, "Artifact · " + name
	})
	return a
}

// Init fetches the directory once and arms the refresh tick. The first
// ArtifactsLoadedMsg populates the list; the tick re-fetches on the interval.
func (a *ArtifactsBrowser) Init() tea.Cmd {
	return tea.Batch(a.fetch(), a.tick())
}

// SetSize sizes the inner area. It reserves the bottom row for the error footer
// when one is showing (so a full list never clips it) and sizes the detail if
// one is open.
func (a *ArtifactsBrowser) SetSize(w, h int) {
	a.Browser.SetSize(w, h)
	a.relayoutList(a.err != nil)
}

// SetTheme re-themes the browser: the list rows bake in the kind hue at snapshot
// time, so a runtime theme switch re-applies the last snapshot to re-resolve
// them (the embedded Browser re-themes itself and any open detail).
func (a *ArtifactsBrowser) SetTheme(th theme.Theme) {
	a.Browser.SetTheme(th)
	err := a.err // re-applying the snapshot is not a successful fetch
	a.applySnapshot(a.last)
	a.err = err
}

// Update folds in the directory snapshots, the refresh tick, and the fetch error,
// then delegates to Browser.Update for navigation and detail delegation.
func (a *ArtifactsBrowser) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ArtifactsLoadedMsg:
		a.applySnapshot(msg.Artifacts)
		return nil
	case artifactsErrMsg:
		// Surface the failure in the footer (fail-loud); the last good snapshot
		// stays visible, and the next successful refresh clears the footer.
		a.err = msg.err
		a.relayoutList(a.err != nil)
		return nil
	case artifactsTickMsg:
		// Re-fetch and re-arm. Both run even before the first snapshot, so a slow
		// first fetch does not stall the refresh loop.
		return tea.Batch(a.fetch(), a.tick())
	}
	return a.Browser.Update(msg)
}

// View renders the list (or the open detail) with a fetch-error footer below it
// when one is showing — kept visible rather than swallowed (fail-loud). At the
// detail level the inner surface owns its own footer, so the fetch error only
// shows at the list.
func (a *ArtifactsBrowser) View() string {
	body := a.Browser.View()
	if a.err != nil && !a.inDetail() {
		return body + "\n" + errorFooter(a.th, a.err, a.w)
	}
	return body
}

// Stop ends the refresh loop (fetch and tick no-op after it) and tears down any
// open reader (its watch). The artifacts browser owns no feed or watch of its own
// — only the poll loop — so beyond stopping the loop it just delegates to
// stopDetail.
func (a *ArtifactsBrowser) Stop() {
	a.stopped = true
	a.stopDetail()
}

// applySnapshot stores a directory snapshot and rebuilds the list rows from it,
// recording each row's artifact name in the parallel names slice so Enter resolves
// by index. ListArtifacts already returns the directory sorted by name, so the
// rows are taken in order. A successful snapshot clears any fetch-error footer
// (the bus is reachable again).
func (a *ArtifactsBrowser) applySnapshot(infos []sextant.ArtifactInfo) {
	items := make([]widget.ListItem, len(infos))
	a.names = make([]string, len(infos))
	for i, info := range infos {
		items[i] = artifactRow(a.th, info)
		a.names[i] = info.Name
	}
	a.last = infos
	a.err = nil
	a.relayoutList(false) // error cleared: restore full list height
	a.setRows(items, a.names)
}

// artifactRow maps one ArtifactInfo to a list row: the name in the default
// foreground, with a dim "rev N" cue trailing the title so the reader can tell at
// a glance how many revisions an artifact has accrued. Revision 0 (an unstamped /
// seeded artifact) omits the cue.
func artifactRow(th theme.Theme, info sextant.ArtifactInfo) widget.ListItem {
	title := info.Name
	if info.Revision > 0 {
		title = fmt.Sprintf("%s  rev %d", info.Name, info.Revision)
	}
	return widget.ListItem{
		Title: title,
		Hue:   th.KindHue(theme.KindArtifactUpdate),
	}
}

// fetch reads the directory off the main loop and returns it as an
// ArtifactsLoadedMsg (or an artifactsErrMsg). It is bounded by a short deadline so
// a wedged bus surfaces an error instead of hanging the refresh. After Stop it is
// a no-op. A nil client (a seeded gallery / golden) skips the fetch — those feed
// ArtifactsLoadedMsg directly.
func (a *ArtifactsBrowser) fetch() tea.Cmd {
	if a.stopped || a.client == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
		defer cancel()
		infos, err := a.client.ListArtifacts(ctx)
		if err != nil {
			return artifactsErrMsg{err: err}
		}
		return ArtifactsLoadedMsg{Artifacts: infos}
	}
}

// tick schedules the next refresh, or nothing after Stop (ending the loop).
func (a *ArtifactsBrowser) tick() tea.Cmd {
	if a.stopped {
		return nil
	}
	return tea.Tick(artifactsRefreshInterval, func(time.Time) tea.Msg { return artifactsTickMsg{} })
}
