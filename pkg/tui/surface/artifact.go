package surface

import (
	"context"
	"fmt"
	"strings"
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
// result of the surface's own GetArtifact (or a watch change) and the seam a test
// or seeded gallery feeds a synthetic document through — the surface renders only
// from the most recent ArtifactLoadedMsg.
type ArtifactLoadedMsg struct {
	// Artifact is the fetched artifact, as returned by client.GetArtifact.
	Artifact sextant.Artifact
}

// artifactErrMsg reports that a fetch failed. The surface keeps the last good
// document and records the error for its footer.
type artifactErrMsg struct {
	err error
}

// ArtifactMode is the artifact surface's mode: reader (render only) or review
// (reader plus a comment-compose affordance).
type ArtifactMode int

const (
	// ModeReader renders the document title + body, scrollable.
	ModeReader ArtifactMode = iota
	// ModeReview is the reader plus a one-line comment compose. A comment reuses
	// the chat.message primitive (no review record type) and is emitted as an
	// OpenMsg/published reference — see the comment-publish convention below.
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
// The surface renders only from the last ArtifactLoadedMsg, so a test feeds it a
// synthetic document without a bus.
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
	revision uint64
	w, h     int
	err      error
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
		client: client,
		ctx:    ctx,
		name:   name,
		theme:  th,
		keys:   keys,
		mode:   cfg.mode,
		detail: widget.NewDetail(keys),
		input:  in,
	}
}

// ID returns the stable layout id.
func (a *Artifact) ID() string { return "artifact" }

// Title returns the pane label: the artifact name, falling back to "artifact".
func (a *Artifact) Title() string {
	if a.name != "" {
		return a.name
	}
	return "artifact"
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

// Init fetches the artifact once. A change-tracking watch is left out of scope
// (M4 documents are read on open); the dash can re-fetch on demand.
func (a *Artifact) Init() tea.Cmd {
	return a.fetch()
}

// Update handles the loaded document, the error case, and — in review mode while
// active — comment composition (Enter publishes a chat.message; Esc steps out).
// Reader scrolling runs on up/down while active.
func (a *Artifact) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ArtifactLoadedMsg:
		a.applyArtifact(msg.Artifact)
		return nil
	case artifactErrMsg:
		// Surface a fetch failure in the footer; keep the last good document.
		a.err = msg.err
		a.relayout()
		return nil
	case publishedMsg:
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

// Stop tears the artifact surface down (the Surface contract's teardown). The
// reader fetches on open and holds no live subscription today, so this no-ops; it
// is the seam a future WatchArtifact lives behind, and keeps teardown uniform
// across surfaces. It is safe to call more than once.
func (a *Artifact) Stop() {}

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

// fetch reads the artifact off the main loop, bounded by a short deadline.
func (a *Artifact) fetch() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
		defer cancel()
		art, err := a.client.GetArtifact(ctx, a.name)
		if err != nil {
			return artifactErrMsg{err: err}
		}
		return ArtifactLoadedMsg{Artifact: art}
	}
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
			return publishedMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
		defer cancel()
		if err := a.client.Publish(ctx, subject, record); err != nil {
			return publishedMsg{err: err}
		}
		return publishedMsg{}
	}
}

// applyArtifact stores a fetched artifact and re-renders its body. A successful
// fetch clears any error footer (and gives its row back to the reader).
func (a *Artifact) applyArtifact(art sextant.Artifact) {
	a.loaded = true
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

// rerender re-lays the document body for the current width and feeds it to the
// Detail widget. A document is rendered as Markdown via glamour (matching the
// prototype); a non-document record is shown raw so nothing is hidden.
func (a *Artifact) rerender() {
	if !a.loaded {
		a.detail.SetText("")
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
