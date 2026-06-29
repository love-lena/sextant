package surface

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/theme"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/widget"
	"github.com/love-lena/sextant/sdk/go"
)

// ArtifactLoadedMsg carries a fetched artifact to the artifact surface. It is the
// seam a test or seeded gallery feeds a synthetic document through — the surface
// renders the most recent applied value. Live updates arrive as artifactChangeMsg
// off the watch; ArtifactLoadedMsg stays the test/gallery injection point.
type ArtifactLoadedMsg struct {
	// Artifact is the fetched artifact, as returned by client.GetArtifact.
	Artifact sextant.Artifact
}

// artifactChangeMsg carries one change off the live watch: a write (the artifact
// at this revision) or a delete (Deleted set, empty record). The surface applies
// it by re-rendering, so an artifact created or updated after launch shows
// without a restart, and one absent at launch is picked up the moment it is
// created (clearing the not-found footer).
//
// owner addresses the change to the surface that opened the watch. The layout
// broadcasts every non-key message to ALL mounted surfaces (a host may run two
// artifact-backed panes — the always-on reader and the detail pane), and these
// messages carry no other identity, so each surface ignores a change whose owner
// is not itself. Without the tag one pane's change would re-render the other and
// multiply the pump.
type artifactChangeMsg struct {
	owner  *Artifact
	change sextant.ArtifactChange
}

// artifactWatchingMsg reports that the watch is open and live. The surface issues
// the first pump step on receiving it, mirroring the stream's SubscribedMsg. It
// is owner-tagged for the same broadcast reason as artifactChangeMsg.
type artifactWatchingMsg struct {
	owner *Artifact
}

// artifactErrMsg reports that opening the watch failed. The surface keeps the
// last good document (if any) and records the error for its footer.
//
// owner addresses the failure to the surface whose watch failed, for the same
// broadcast reason as artifactChangeMsg: without the tag one pane's watch
// failure would footer every artifact reader. A nil owner is an untagged,
// test-synthesized message, claimed by whichever surface it is fed to.
type artifactErrMsg struct {
	owner *Artifact
	err   error
}

// Artifact is the artifact surface (ADR-0023): a document reader on the Detail
// widget. It renders the document's Markdown body (via glamour, matching the
// prototype look) and scrolls.
//
// The reader live-updates over client.WatchArtifact: Init opens a watch that
// delivers the current value (if present) then every subsequent change, so a
// document created or updated after launch refreshes without a restart and a
// delete shows a removed state. A test feeds it a synthetic document through
// ArtifactLoadedMsg without a bus.
type Artifact struct {
	client *sextant.Client
	ctx    context.Context
	name   string
	theme  theme.Theme
	keys   theme.Keymap

	detail widget.Detail
	focus  widget.Focus

	doc      document
	hasDoc   bool
	rawBody  string // the artifact's record when it is not a document
	loaded   bool
	deleted  bool // the watched artifact was removed (a delivered delete)
	revision uint64
	w, h     int
	err      error

	// changes bridges the WatchArtifact handler (a bus delivery goroutine) into
	// the Bubble Tea loop: the handler does a non-blocking send, the pump reads one
	// change per step. mu guards the watch handle and the stopped flag, the same
	// teardown discipline busfeed uses, so Stop is goleak-clean and idempotent.
	changes chan sextant.ArtifactChange
	mu      sync.Mutex
	watch   sextant.Watch
	stopped bool
}

// NewArtifact builds an artifact surface for the named artifact. Pass a context
// that lives as long as the surface and the resolved theme/keymap.
func NewArtifact(ctx context.Context, client *sextant.Client, name string, th theme.Theme, keys theme.Keymap) *Artifact {
	return &Artifact{
		client:  client,
		ctx:     ctx,
		name:    name,
		theme:   th,
		keys:    keys,
		detail:  widget.NewDetail(keys),
		changes: make(chan sextant.ArtifactChange, watchBuffer),
	}
}

// watchBuffer is the capacity of the channel between the WatchArtifact handler
// and the pump. An artifact watch is low-volume (one change per write), so a
// small buffer absorbs a burst without blocking the delivery goroutine.
const watchBuffer = 16

// ID returns the stable layout id.
func (a *Artifact) ID() string { return "artifact" }

// Title returns the pane label: the surface type plus its target name, e.g.
// "Artifact · the-plan", so the chrome reads as a document and is distinguishable
// from a same-named stream pane. With no name the label is the bare type. The Box
// title chip truncates it in a narrow pane.
func (a *Artifact) Title() string {
	if a.name != "" {
		return "Artifact · " + a.name
	}
	return "Artifact"
}

// SetSize sizes the inner reader area and re-renders the body to the new width.
func (a *Artifact) SetSize(w, h int) {
	a.w, a.h = w, h
	a.relayout()
	a.rerender()
}

// relayout sizes the reader to the inner area minus the error-footer row (when
// an error is showing).
func (a *Artifact) relayout() {
	readerH := a.h
	if a.err != nil {
		readerH--
	}
	if readerH < 1 {
		readerH = 1
	}
	a.detail.SetSize(a.w, readerH)
}

// SetTheme re-themes the surface: it stores the new theme and rebuilds the
// rendered body, because the Markdown reader's glamour style is chosen from the
// theme variant (light/dark) at render time. The detail widget itself takes the
// theme at View time, so the title/hue follow on the next render; rerender keeps
// the body's glamour styling in step with the variant.
func (a *Artifact) SetTheme(th theme.Theme) {
	a.theme = th
	a.rerender()
}

// SetFocus sets the three-state focus.
func (a *Artifact) SetFocus(f widget.Focus) {
	a.focus = f
}

// CapturingText reports whether the surface is capturing typed text. The reader
// has no text input, so it never captures.
func (a *Artifact) CapturingText() bool {
	return false
}

// Init opens a live watch on the artifact. WatchArtifact delivers the current
// value first (if the artifact exists), then every subsequent change, so the
// reader live-updates without a restart: a document created or updated after
// launch refreshes, and one absent at launch is picked up the moment it is
// created. The watch runs on a bus delivery goroutine that does a non-blocking
// send onto the change buffer; the pump (nextChange) reads one change per step
// off the main loop, mirroring the stream feed. A nil client (the goldens) skips
// the watch — those feed state through ArtifactLoadedMsg directly.
func (a *Artifact) Init() tea.Cmd {
	if a.client == nil {
		return nil
	}
	return func() tea.Msg {
		w, err := a.client.WatchArtifact(a.ctx, a.name, a.handle)
		if err != nil {
			return artifactErrMsg{owner: a, err: err}
		}
		a.mu.Lock()
		// If Stop already ran (or ctx is gone), don't hold a live watch.
		if a.stopped {
			a.mu.Unlock()
			_ = w.Stop()
			return nil
		}
		a.watch = w
		a.mu.Unlock()
		return artifactWatchingMsg{owner: a}
	}
}

// handle is the WatchArtifact handler. It runs on a bus delivery goroutine and
// must never block, so it sends non-blocking: a full buffer drops the change (the
// next delivered change still reflects the live value, so a dropped intermediate
// revision is harmless for a reader). The send is gated by stopped under the
// mutex so a delivery racing Stop never reaches a closed channel.
func (a *Artifact) handle(ch sextant.ArtifactChange) {
	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		return
	}
	select {
	case a.changes <- ch:
	default:
	}
	a.mu.Unlock()
}

// nextChange is the pump step: it reads one change off the buffer, off the main
// loop, and returns it as an artifactChangeMsg. The surface returns it again on
// every artifactChangeMsg to keep the pump running. It returns nil when the watch
// is stopped and the buffer is drained, ending the pump cleanly.
func (a *Artifact) nextChange() tea.Cmd {
	return func() tea.Msg {
		ch, ok := <-a.changes
		if !ok {
			return nil
		}
		return artifactChangeMsg{owner: a, change: ch}
	}
}

// Update handles the loaded document and the error case. Reader scrolling runs
// on up/down while active.
func (a *Artifact) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ArtifactLoadedMsg:
		a.applyArtifact(msg.Artifact)
		return nil
	case artifactWatchingMsg:
		// Broadcast to every surface: ignore another artifact pane's watch.
		if msg.owner != a {
			return nil
		}
		// Watch is open and live; start the pump.
		return a.nextChange()
	case artifactChangeMsg:
		// Broadcast to every surface: ignore another artifact pane's change.
		if msg.owner != a {
			return nil
		}
		a.applyChange(msg.change)
		return a.nextChange() // keep pumping
	case artifactErrMsg:
		// Broadcast to every surface: ignore another artifact pane's watch failure
		// (an untagged one — nil owner, test-synthesized — counts as this pane's).
		if msg.owner != nil && msg.owner != a {
			return nil
		}
		// Surface a watch failure in the footer; keep the last good document.
		a.err = msg.err
		a.relayout()
		return nil
	case tea.KeyMsg:
		return a.handleKey(msg)
	}
	return nil
}

// handleKey routes a key while active: scroll the reader on up/down. The
// bindings come from the keymap (keys are data), not literal strings, so a
// rebind is honoured here as it is in the chrome and the detail widget. Back is
// a no-op — the reader is a single level (ADR-0026); a hosting browser consumes
// Esc to pop it.
func (a *Artifact) handleKey(msg tea.KeyMsg) tea.Cmd {
	if a.focus != widget.FocusActive {
		return nil
	}
	switch {
	case key.Matches(msg, a.keys.Up), key.Matches(msg, a.keys.Down):
		a.detail, _ = a.detail.Update(msg)
		return nil
	}
	return nil
}

// View renders the reader and an error footer below it when a fetch failed —
// kept visible rather than swallowed (fail-loud).
func (a *Artifact) View() string {
	parts := []string{a.detail.View(a.theme, a.focus)}
	if a.err != nil {
		parts = append(parts, errorFooter(a.theme, a.err, a.w))
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// Stop tears the artifact watch down (the Surface contract's teardown): it stops
// the SDK watch and closes the change buffer so a blocked pump unblocks and
// returns nil, ending the pump. The layout calls it when unmounting the surface;
// a standalone host calls it on quit. It is safe to call more than once and safe
// to call before the watch finishes opening; after Stop no goroutine or
// subscription survives (goleak-clean).
func (a *Artifact) Stop() {
	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		return
	}
	a.stopped = true
	w := a.watch
	a.watch = nil
	a.mu.Unlock()

	if w != nil {
		_ = w.Stop()
	}
	close(a.changes)
}

// applyChange applies one watch change: a write renders the new value (and clears
// any not-found footer the absent-at-launch case left), a delete shows a removed
// state so the reader sees the artifact go rather than freezing on the last value.
func (a *Artifact) applyChange(ch sextant.ArtifactChange) {
	if ch.Deleted {
		a.applyDeleted()
		return
	}
	a.applyArtifact(ch.Artifact)
}

// applyArtifact stores a fetched artifact and re-renders its body. A successful
// fetch clears any error footer (and gives its row back to the reader) — so an
// artifact absent at launch (which left a not-found footer) recovers cleanly the
// moment it is created.
func (a *Artifact) applyArtifact(art sextant.Artifact) {
	a.loaded = true
	a.deleted = false
	hadErr := a.err != nil
	a.err = nil
	a.revision = art.Revision
	if doc, ok := parseDocument(art.Record); ok {
		a.doc = doc
		a.hasDoc = true
		a.rawBody = ""
	} else {
		a.hasDoc = false
		a.rawBody = string(art.Record)
	}
	if hadErr {
		a.relayout()
	}
	a.rerender()
}

// applyDeleted records that the watched artifact was removed and re-renders a
// removed state, so the reader sees the artifact go rather than freezing on its
// last value. A later re-create flows back through applyArtifact.
func (a *Artifact) applyDeleted() {
	a.loaded = true
	a.deleted = true
	a.hasDoc = false
	a.rawBody = ""
	a.revision = 0
	a.rerender()
}

// rerender re-lays the document body for the current width and feeds it to the
// Detail widget. A document is rendered as Markdown via glamour (matching the
// prototype); a non-document record is shown raw so nothing is hidden.
func (a *Artifact) rerender() {
	if !a.loaded {
		a.detail.SetText("")
		return
	}
	if a.deleted {
		a.detail.SetText(lipgloss.NewStyle().Foreground(a.theme.Dim).Render("(artifact deleted)"))
		return
	}
	if !a.hasDoc {
		a.detail.SetText("(non-document artifact)\n\n" + a.rawBody)
		return
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(a.theme.Title).Render(a.doc.Title)
	// A revision cue tells the reader which version they are looking at (artifacts
	// are compare-and-set versioned; ADR-0005). Revision 0 is "unstamped" — a
	// synthetic/seeded document — so it is omitted.
	if a.revision > 0 {
		rev := lipgloss.NewStyle().Foreground(a.theme.Dim).Render(fmt.Sprintf("  rev %d", a.revision))
		title += rev
	}
	body := renderMarkdown(a.doc.Body, a.w, a.theme.Variant)
	a.detail.SetText(title + "\n\n" + body)
}

// renderMarkdown renders Markdown to width via glamour, using the standard light
// or dark style to match the dash theme so the reader stays legible in both. The
// style and the pinned TrueColor profile make the output deterministic for
// goldens. It word-wraps a touch under width to leave room for glamour's document
// margin, and falls back to the raw body if rendering fails. Trailing blank lines
// glamour adds are trimmed so the reader does not open on empty space.
func renderMarkdown(md string, width int, variant theme.Variant) string {
	if width < 4 {
		width = 4
	}
	wrap := width - 2
	if wrap < 1 {
		wrap = 1
	}
	style := "dark"
	if variant == theme.VariantLight {
		style = "light"
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(style),
		glamour.WithWordWrap(wrap),
	)
	if err != nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return strings.Trim(out, "\n")
}
