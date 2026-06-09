package surface

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
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

// ArtifactMode is the artifact surface's mode: reader (render only) or review
// (reader plus a comment-compose affordance).
type ArtifactMode int

const (
	// ModeReader renders the document title + body, scrollable.
	ModeReader ArtifactMode = iota
	// ModeReview is the reader plus a one-line comment compose. A comment reuses
	// the chat.message primitive (no review record type) — see the
	// comment-publish convention below.
	ModeReview
)

// Artifact is the artifact surface (ADR-0023): a document reader on the Detail
// widget, with an optional thin review affordance. Reader mode renders the
// document's Markdown body (via glamour, matching the prototype look). Review
// mode adds a one-line comment compose that reuses the chat.message primitive —
// a review comment is a chat.message whose replyTo is the artifact name,
// published to the artifact's comment subject (sx.TopicSubject("artifact." +
// name)); the surface keeps no threaded-comment model of its own (primitives,
// not policy).
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
	mode   ArtifactMode

	detail widget.Detail
	input  textinput.Model
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

// ArtifactOption configures an Artifact surface.
type ArtifactOption func(*artifactConfig)

type artifactConfig struct {
	mode ArtifactMode
}

// WithReview puts the surface in review mode: the reader plus a comment compose.
// Without it the surface is a plain reader.
func WithReview() ArtifactOption {
	return func(c *artifactConfig) { c.mode = ModeReview }
}

// NewArtifact builds an artifact surface for the named artifact. Pass a context
// that lives as long as the surface, the resolved theme/keymap, and any options
// (WithReview for review mode).
func NewArtifact(ctx context.Context, client *sextant.Client, name string, th theme.Theme, keys theme.Keymap, opts ...ArtifactOption) *Artifact {
	var cfg artifactConfig
	for _, o := range opts {
		o(&cfg)
	}
	in := textinput.New()
	in.Prompt = "comment> "
	in.Placeholder = "leave a comment…"
	return &Artifact{
		client:  client,
		ctx:     ctx,
		name:    name,
		theme:   th,
		keys:    keys,
		mode:    cfg.mode,
		detail:  widget.NewDetail(keys),
		input:   in,
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

// SetSize sizes the inner reader area, reserving the bottom row for the comment
// line in review mode and another for the error footer when one is showing. It
// re-renders the body to the new width.
func (a *Artifact) SetSize(w, h int) {
	a.w, a.h = w, h
	if a.mode == ModeReview && w > 0 {
		a.input.Width = w - lipgloss.Width(a.input.Prompt) - 1
		if a.input.Width < 1 {
			a.input.Width = 1
		}
	}
	a.relayout()
	a.rerender()
}

// relayout sizes the reader to the inner area minus the comment row (review mode)
// and the error-footer row (when an error is showing). It re-renders the body
// because the glamour wrap depends on the reader width, which is unchanged here —
// only the height moves — so it does not re-run the markdown render.
func (a *Artifact) relayout() {
	readerH := a.h
	if a.mode == ModeReview {
		readerH--
	}
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

// SetFocus sets the three-state focus; in review mode, active focuses the
// comment input.
func (a *Artifact) SetFocus(f widget.Focus) {
	a.focus = f
	if a.mode != ModeReview {
		return
	}
	if f == widget.FocusActive {
		a.input.Focus()
	} else {
		a.input.Blur()
	}
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

// Update handles the loaded document, the error case, and — in review mode while
// active — comment composition (Enter publishes a chat.message; Esc steps out).
// Reader scrolling runs on up/down while active.
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
	case publishedMsg:
		// Broadcast to every surface: claim only this pane's own comment result.
		if !msg.ownedBy(a) {
			return nil
		}
		// A failed comment publish surfaces in the footer; a success clears it.
		a.err = msg.err
		a.relayout()
		return nil
	case tea.KeyMsg:
		return a.handleKey(msg)
	}
	return nil
}

// handleKey routes a key while active: scroll the reader, and in review mode
// edit/submit the comment. The bindings come from the keymap (keys are data),
// not literal strings, so a rebind is honoured here as it is in the chrome and
// the detail widget. Back steps out (DoneMsg).
func (a *Artifact) handleKey(msg tea.KeyMsg) tea.Cmd {
	if a.focus != widget.FocusActive {
		return nil
	}
	switch {
	case key.Matches(msg, a.keys.Back):
		a.input.SetValue("")
		a.input.Blur()
		return doneCmd(a.ID())
	case key.Matches(msg, a.keys.Up), key.Matches(msg, a.keys.Down):
		a.detail, _ = a.detail.Update(msg)
		return nil
	case key.Matches(msg, a.keys.Enter):
		if a.mode != ModeReview {
			return nil
		}
		text := strings.TrimSpace(a.input.Value())
		if text == "" {
			return nil
		}
		a.input.SetValue("")
		return a.comment(text)
	}
	if a.mode == ModeReview {
		var cmd tea.Cmd
		a.input, cmd = a.input.Update(msg)
		return cmd
	}
	return nil
}

// View renders the reader, the comment line below it in review mode, and an
// error footer below that when a fetch or comment publish failed — kept visible
// rather than swallowed (fail-loud).
func (a *Artifact) View() string {
	parts := []string{a.detail.View(a.theme, a.focus)}
	if a.mode == ModeReview {
		parts = append(parts, a.commentLine())
	}
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

// commentLine renders the review comment row: the live input when active, a dim
// hint otherwise.
func (a *Artifact) commentLine() string {
	if a.focus == widget.FocusActive {
		return a.input.View()
	}
	w := a.w
	if w <= 0 {
		w = 1
	}
	return lipgloss.NewStyle().Foreground(a.theme.Dim).Width(w).MaxWidth(w).Render("comment> enter to review")
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

// comment publishes a review comment as a chat.message referencing the artifact:
// replyTo is the artifact name, published to the artifact's comment subject. This
// reuses the chat.message primitive rather than inventing a review record type.
func (a *Artifact) comment(text string) tea.Cmd {
	subject := artifactCommentSubject(a.name)
	name := a.name
	return func() tea.Msg {
		record, err := marshalChatMessage(text, name)
		if err != nil {
			return publishedMsg{owner: a, err: err}
		}
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
		defer cancel()
		if err := a.client.Publish(ctx, subject, record); err != nil {
			return publishedMsg{owner: a, err: err}
		}
		return publishedMsg{owner: a}
	}
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

// artifactCommentSubject is the subject a review comment publishes to: the
// artifact's topic, msg.topic.artifact.<name>, built through sx.TopicSubject so
// the subject convention has one source of truth. It is a naming convention over
// the messages space (ADR-0023), not a bus construct — a reviewer subscribes to
// it to see comments on an artifact.
func artifactCommentSubject(name string) string {
	return sx.TopicSubject("artifact." + name)
}
