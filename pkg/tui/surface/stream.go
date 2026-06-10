package surface

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
	"github.com/rivo/uniseg"

	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/tui/busfeed"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// authorWidth is the fixed column the author label occupies in a rendered line,
// so the text bodies align down the stream regardless of name length.
const authorWidth = 14

// MaxStreamEntries caps the entry log a Stream retains — and with it the
// rendered-lines buffer, which replays from the log. The feed subscribes with
// DeliverAll, so a busy topic's full retained history loads on open, and an
// open conversation holds its place for the dash's whole life (ADR-0026);
// without a cap both the logged frames (record bytes included) and the rendered
// lines grow forever. Past the cap the oldest entries are dropped and the
// buffer renders behind an honest "older history trimmed" marker (mirroring the
// drop-gap marker), so the operator knows the view is truncated. It is an
// overridable package-level default, not a hard policy — set it before
// constructing streams to retain more (or less); zero or negative disables
// trimming.
var MaxStreamEntries = 4000

// Author resolves a frame author id to what the stream renders: a display name
// and a role (e.g. "agent", "human"), so the line shows a readable name in the
// author's role hue. It is the resolution unit a WithAuthors map carries.
type Author struct {
	// Name is the human display name; empty falls back to the short author id.
	Name string
	// Role is the client role the stream hues the author by; an unknown or empty
	// role falls back to the default foreground (theme.RoleHue's own fallback).
	Role string
}

// StreamOption configures a Stream surface.
type StreamOption func(*streamConfig)

type streamConfig struct {
	compose bool
	authors map[string]Author // client id -> display name + role
}

// WithCompose turns on the compose affordance: the surface becomes the "chat"
// config (participate) — while the surface is the focused pane the operator
// types a line and Enter publishes it. Without it the surface is "tail"
// (observe only) — the same one read-stream, no send side (ADR-0023).
func WithCompose() StreamOption {
	return func(c *streamConfig) { c.compose = true }
}

// WithAuthors supplies a client-id → Author map so the stream renders a readable
// display name in the author's role hue instead of a raw id in the default
// colour. It is the resolution seam ADR-0023 leaves open: a frame carries only
// the author id, so the dash builds this map from presence (id → name + kind)
// and passes it in; the surface never reaches for presence itself. Without it
// the stream shows a short author id in the default foreground — the documented
// fallback.
func WithAuthors(authors map[string]Author) StreamOption {
	return func(c *streamConfig) {
		c.authors = make(map[string]Author, len(authors))
		for k, v := range authors {
			c.authors[k] = v
		}
	}
}

// Stream is the message surface (ADR-0023): one read-stream plus an optional
// compose. The read side is a busfeed.Feed subscription on a subject; each
// chat.message frame is rendered author + text, hued by author. The send side is
// the compose line — when enabled and the surface is active, typing composes a
// line and Enter publishes a chat.message to the SAME subject. Read and send
// merge by round-trip: a sent line appears only when the bus echoes it back on
// the subscription. There is no optimistic echo.
//
// The surface renders from EventMsgs, so a test feeds it synthetic busfeed
// events without a bus. Esc is a no-op at this surface's level (ADR-0026: a
// stream opened as a browser's detail is popped by the BROWSER consuming Esc;
// standalone, the host quits on its own keys).
type Stream struct {
	client  *sextant.Client
	ctx     context.Context
	subject string
	theme   theme.Theme
	keys    theme.Keymap

	feed    *busfeed.Feed
	stream  widget.Stream
	input   widget.Compose
	compose bool
	authors map[string]Author

	// entries is the ordered log of what the stream has shown — each a received
	// frame or a coalesced drop-gap — kept so SetTheme can re-render the buffer
	// with the new palette (a rendered line bakes in the author hue, so a runtime
	// theme switch must replay the log to re-hue it). It is bounded by
	// MaxStreamEntries; trimmed counts what fell off the front.
	entries []streamEntry
	// trimmed is the total number of messages dropped from the front of the
	// entry log to hold MaxStreamEntries, rendered as the trim marker at the top
	// of the buffer so the truncation is honest, never silent.
	trimmed int
	// firstLines maps each entry to the index of its first rendered line in the
	// widget's CURRENT buffer (parallel to entries; the trim marker, when shown,
	// occupies the line above firstLines[0]). replay reads it to re-anchor a
	// scrolled-back view on the entry that was topmost (ADR-0026: panes hold
	// their place through a rewrap or a retheme), then rebuilds it.
	firstLines []int
	// lineCount is the widget buffer's current length, kept in step with
	// firstLines so an append can record the new entry's first-line index.
	lineCount int

	focus widget.Focus
	// w, h is the inner area; the compose row, when shown, takes the last line.
	w, h int
	err  error
	// notice holds a transient non-fatal feed notice (a deferred resume:
	// delivery is stalled until the next reconnect). It renders as a footer like
	// err but, unlike err, the feed is still pumping — so the next delivered
	// event clears it (the stall is over).
	notice error
}

// NewStream builds a message surface subscribing to subject. Pass a context that
// lives as long as the surface, the resolved theme/keymap, and any options
// (WithCompose to participate, WithAuthors to resolve authors). The subject is
// typically a topic (sx.TopicSubject("plan")) or a direct subject.
func NewStream(ctx context.Context, client *sextant.Client, subject string, th theme.Theme, keys theme.Keymap, opts ...StreamOption) *Stream {
	var cfg streamConfig
	for _, o := range opts {
		o(&cfg)
	}
	in := widget.NewCompose()
	in.SetPlaceholder("message…")
	in.SetWidth(1) // will be resized by SetSize; initialise to a safe default
	s := &Stream{
		client:  client,
		ctx:     ctx,
		subject: subject,
		theme:   th,
		keys:    keys,
		feed:    busfeed.New(client, subject, sextant.DeliverAll()),
		stream:  widget.NewStream(keys),
		input:   in,
		compose: cfg.compose,
		authors: cfg.authors,
	}
	return s
}

// ID returns the stable layout id.
func (s *Stream) ID() string { return "stream" }

// Title returns the pane label: the surface type plus its target topic, e.g.
// "Stream · plan", so the chrome reads as a chat stream and is distinguishable
// from a same-named document pane. The topic is the subject's trailing segment;
// with no subject the label is the bare type. The Box title chip truncates it in
// a narrow pane.
func (s *Stream) Title() string {
	if topic := s.topic(); topic != "" {
		return "Stream · " + topic
	}
	return "Stream"
}

// topic returns the subject's trailing segment (the topic name), or "" when there
// is no subject.
func (s *Stream) topic() string {
	if i := strings.LastIndex(s.subject, "."); i >= 0 && i < len(s.subject)-1 {
		return s.subject[i+1:]
	}
	return s.subject
}

// SetSize sizes the inner area. The compose width is set to w so it wraps at
// the pane's inner width; height is dynamic (compose height is subtracted in
// relayout). A width change reflows the buffer: message lines soft-wrap to the
// content width (renderEntry), so a narrow/wide resize must re-wrap every
// logged entry.
func (s *Stream) SetSize(w, h int) {
	widthChanged := w != s.w
	s.w, s.h = w, h
	if w > 0 {
		s.input.SetWidth(w)
	}
	if widthChanged {
		s.replay(0)
	}
	s.relayout()
}

// relayout sizes the stream viewport to the inner area minus the compose's
// current height (if compose is on — the compose grows as the operator types,
// so the stream body shrinks to match) and the footer rows (a fatal error
// and/or a transient reconnect notice, when showing), so none overlaps the
// stream.
func (s *Stream) relayout() {
	streamH := s.h
	if s.compose {
		streamH -= s.input.Height()
	}
	if s.err != nil {
		streamH--
	}
	if s.notice != nil {
		streamH--
	}
	if streamH < 1 {
		streamH = 1
	}
	s.stream.SetSize(s.w, streamH)
}

// SetTheme re-themes the surface: it stores the new theme and re-renders the
// whole stream buffer from the entry log. A rendered line bakes in the author's
// role hue (resolved when the frame arrived), so a runtime theme switch must
// replay the log to re-hue every line; the widget itself takes the theme at View
// time for its own chrome (scroll cues), but the per-line hues live in the lines.
func (s *Stream) SetTheme(th theme.Theme) {
	s.theme = th
	s.replay(0)
}

// replay re-renders the whole entry log to stream lines for the current theme and
// width, behind the trim marker when older history has been dropped. It is called
// when the palette OR the inner width changes (a theme switch, a reflow) and when
// the log is trimmed, since the rendered lines depend on the current state: a
// wrapped line wraps to the current content width, a narrower width must reflow
// the buffer to fewer columns, and a trim moves the buffer's head. Replaying from
// the bounded log is also what bounds the widget's rendered-lines buffer.
//
// The scroll state survives the rebuild (ADR-0026: panes hold their place). A
// tail-following view re-pins; a scrolled-back view re-anchors on the ENTRY that
// was topmost before the replay, scrolling so its first rendered line is topmost
// after — entry-anchored, not line-anchored, so the anchor is stable across a
// rewrap (sub-entry position through a rewrap is deliberately approximate: a
// long entry may shift within itself). anchorShift is how many entries the
// caller just dropped from the log's front (a trim), so an anchor read against
// the old rendering maps onto the new indices; an anchor trimmed away clamps to
// the oldest surviving entry.
func (s *Stream) replay(anchorShift int) {
	// Read the widget's scroll state BEFORE the rebuild mutates it.
	anchored := !s.stream.Following()
	anchor := -1 // -1: the trim marker (or an empty log) was topmost; keep the top
	if anchored {
		if anchor = s.entryAt(s.stream.Offset()); anchor >= 0 {
			anchor -= anchorShift
			if anchor < 0 {
				anchor = 0 // the anchored entry was trimmed away: oldest survivor
			}
		}
	}

	lines := make([]string, 0, s.lineCount)
	s.firstLines = s.firstLines[:0]
	if s.trimmed > 0 {
		lines = append(lines, s.trimMarker(s.trimmed))
	}
	for _, e := range s.entries {
		s.firstLines = append(s.firstLines, len(lines))
		lines = append(lines, s.renderEntry(e)...)
	}
	s.lineCount = len(lines)
	s.stream.SetLines(lines) // holds the follow state; re-pins only while following
	if anchored {
		target := 0
		if anchor >= 0 && anchor < len(s.firstLines) {
			target = s.firstLines[anchor]
		}
		s.stream.ScrollTo(target)
	}
}

// entryAt returns the index of the entry whose rendering covers the given line
// of the widget's CURRENT buffer (per the firstLines mapping), or -1 when the
// line sits above the first entry (the trim marker row) or the log is empty.
func (s *Stream) entryAt(line int) int {
	idx := -1
	for i, fl := range s.firstLines {
		if fl > line {
			break
		}
		idx = i
	}
	return idx
}

// appendEntry logs one entry and renders it into the stream. When the log
// exceeds MaxStreamEntries it trims the oldest entries and replays the bounded
// log behind the trim marker (shifting any scroll-back anchor by the entries
// dropped); otherwise the new entry's lines append directly, extending the
// line↔entry mapping.
func (s *Stream) appendEntry(e streamEntry) {
	s.entries = append(s.entries, e)
	if dropped := s.trim(); dropped > 0 {
		s.replay(dropped)
		return
	}
	s.firstLines = append(s.firstLines, s.lineCount)
	lines := s.renderEntry(e)
	s.lineCount += len(lines)
	s.stream.Append(lines...)
}

// trim drops the oldest entries once the log exceeds MaxStreamEntries, keeping
// a tenth of slack under the cap so the full-buffer replay is paid once per
// batch rather than on every message at the cap. The trimmed count accumulates
// what the marker reports — a trimmed drop-gap contributes the messages it
// stood for, so the total stays honest. It returns how many ENTRIES were
// dropped (0 when nothing was), which is the shift a scroll-back anchor needs.
func (s *Stream) trim() int {
	limit := MaxStreamEntries
	if limit <= 0 || len(s.entries) <= limit {
		return 0
	}
	keep := limit - limit/10
	if keep < 1 {
		keep = 1
	}
	dropped := len(s.entries) - keep
	for _, e := range s.entries[:dropped] {
		if e.hasFrame {
			s.trimmed++
		} else {
			s.trimmed += e.dropped
		}
	}
	s.entries = s.entries[dropped:]
	return dropped
}

// streamEntry is one logged item the stream has shown: a received frame, or a
// coalesced drop-gap of n events. The log is kept so SetTheme can re-render the
// buffer with a new palette.
type streamEntry struct {
	frame    sextant.Message
	hasFrame bool
	dropped  int
}

// renderEntry renders one logged entry to one OR MORE stream lines for the
// current theme and width: a frame through renderFrame (soft-wrapped, so a long
// message spans several lines rather than clipping), a drop-gap through
// dropMarker. The widget still truncates each line as a safety net, but a normal
// message now wraps to the content width instead of running off the right edge.
func (s *Stream) renderEntry(e streamEntry) []string {
	if e.hasFrame {
		return s.renderFrame(e.frame)
	}
	return []string{s.dropMarker(e.dropped)}
}

// SetFocus sets the three-state focus. Gaining focus (active) focuses the
// compose input when compose is on; losing it blurs the input (the typed text
// holds — the pane keeps its place while the operator works elsewhere).
func (s *Stream) SetFocus(f widget.Focus) {
	s.focus = f
	if !s.compose {
		return
	}
	if f == widget.FocusActive {
		_ = s.input.Focus() // returns a cursor-blink cmd; irrelevant for surface routing
	} else {
		s.input.Blur()
	}
}

// CapturingText reports whether the compose input is live (compose on and
// focused — the surface is the focused pane), so a host delivers printable keys
// here instead of acting on them as shortcuts.
func (s *Stream) CapturingText() bool {
	return s.compose && s.input.Focused()
}

// Init opens the feed. The pump runs from Update: every EventMsg, DroppedMsg,
// and ResumeDeferredMsg re-issues Next. A nil client (a seeded gallery /
// golden) skips the subscribe — those feed events directly, the same
// convention the artifact surface follows.
func (s *Stream) Init() tea.Cmd {
	if s.client == nil {
		return nil
	}
	return s.feed.Subscribe(s.ctx)
}

// Update drives the feed pump, renders incoming frames, and — when active and
// composing — handles typing and Enter (publish).
//
// Several feeds can be live in one program (a topics-discovery wildcard, another
// pane's open conversation), and every message reaches every surface in its
// path, so the stream claims only busfeed messages tagged by ITS feed. An
// untagged message (nil From — test-synthesized) is treated as its own.
func (s *Stream) Update(msg tea.Msg) tea.Cmd {
	if from := busfeed.From(msg); from != nil && from != s.feed {
		return nil // another feed's traffic; not this conversation's
	}
	switch msg := msg.(type) {
	case busfeed.SubscribedMsg:
		// Subscription is live; start the pump.
		return s.feed.Next()
	case busfeed.EventMsg:
		if s.notice != nil {
			// Events are flowing again: the deferred resume succeeded, so the
			// transient reconnect notice auto-clears (mirroring how a drop-gap is
			// a one-off marker, not a sticky state).
			s.notice = nil
			s.relayout()
		}
		s.appendEntry(streamEntry{frame: msg.Message, hasFrame: true})
		return s.feed.Next() // keep pumping
	case busfeed.DroppedMsg:
		s.appendEntry(streamEntry{dropped: msg.N})
		return s.feed.Next() // DroppedMsg is not terminal; keep pumping
	case busfeed.ResumeDeferredMsg:
		// NOT terminal: delivery is stalled until the next reconnect retries the
		// resume, but the subscription is still registered. Show the transient
		// notice and keep pumping — the pump is what delivers the recovery.
		s.notice = msg.Err
		s.relayout()
		return s.feed.Next()
	case busfeed.ErrMsg:
		// Terminal: the feed stops reading. Surface the error in the footer.
		s.err = msg.Err
		s.relayout()
		return nil
	case publishedMsg:
		// Broadcast to every surface: claim only this stream's own publish result
		// (an untagged one — nil owner, test-synthesized — counts as its own).
		if !msg.ownedBy(s) {
			return nil
		}
		// A failed publish surfaces in the footer; a success clears any prior one.
		// Either way the sent line appears via the round-trip echo, not here.
		s.err = msg.err
		s.relayout()
		return nil
	case tea.KeyMsg:
		return s.handleKey(msg)
	}
	return nil
}

// handleKey routes a key while the surface is active: scrolling, plus compose
// when enabled. The bindings come from the keymap (keys are data), not literal
// strings, so an operator's rebind is honoured here as it is in the chrome and
// the inner widget. Enter publishes the composed line; the scroll bindings
// review the backlog; other keys edit the compose buffer. Back is a no-op —
// the stream is a single level (ADR-0026); a hosting browser consumes Esc to
// pop it.
//
// While the compose is capturing, every printable key is TEXT before it is a
// binding: j/k share the scroll bindings (and q is the host's quit key), so a
// text key routes straight to the input — only control keys (arrows, Enter)
// can match bindings mid-compose.
func (s *Stream) handleKey(msg tea.KeyMsg) tea.Cmd {
	if s.focus != widget.FocusActive {
		return nil
	}
	if s.CapturingText() && isTextKey(msg) {
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(msg)
		s.relayout() // compose may have grown or shrunk
		return cmd
	}
	switch {
	case key.Matches(msg, s.keys.Enter):
		if !s.compose {
			return nil
		}
		text := strings.TrimSpace(s.input.Value())
		if text == "" {
			return nil
		}
		s.input.SetValue("")
		s.relayout() // compose shrank back to 1 row on clear
		return s.publish(text)
	case key.Matches(msg, s.keys.Up), key.Matches(msg, s.keys.Down):
		// Scrolling the stream takes precedence over compose history, so up/down
		// review the backlog.
		s.stream, _ = s.stream.Update(msg)
		return nil
	}
	if s.compose {
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(msg)
		s.relayout() // compose may have grown or shrunk
		return cmd
	}
	return nil
}

// View renders the stream, the compose line below it when compose is on, and a
// footer below that when something needs saying — a fatal error (subscribe or
// publish failed) and/or the transient reconnect notice — kept visible rather
// than swallowed (fail-loud).
func (s *Stream) View() string {
	parts := []string{s.stream.View(s.theme, s.focus)}
	if s.compose {
		parts = append(parts, s.composeLine())
	}
	if s.notice != nil {
		parts = append(parts, errorFooter(s.theme, s.notice, s.w))
	}
	if s.err != nil {
		parts = append(parts, errorFooter(s.theme, s.err, s.w))
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// composeLine renders the compose input. The Compose widget handles both the
// live input (active focus) and the dim placeholder (unfocused), so this is a
// straight delegation. Height is dynamic — Height() rows are rendered.
func (s *Stream) composeLine() string {
	return s.input.View(s.theme, s.focus)
}

// publish marshals the typed text as a chat.message and publishes it to the
// subscribed subject, off the main loop. The result is a publishedMsg carrying
// any error; the message itself appears via the feed echo (round-trip), never a
// local append.
func (s *Stream) publish(text string) tea.Cmd {
	return func() tea.Msg {
		record, err := marshalChatMessage(text, "")
		if err != nil {
			return publishedMsg{owner: s, err: err}
		}
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		defer cancel()
		if err := s.client.Publish(ctx, s.subject, record); err != nil {
			return publishedMsg{owner: s, err: err}
		}
		return publishedMsg{owner: s}
	}
}

// publishedMsg reports the outcome of a compose/comment publish. Success carries
// no data (the line round-trips back through the feed); a failure carries the
// error.
//
// owner addresses the result to the surface that issued the publish. The layout
// broadcasts every non-key message to ALL mounted panes, and several publishing
// surfaces can be live at once (a DM, a topic conversation, an artifact review),
// so without the tag one pane's publish failure would footer every conversation
// — and one pane's success would clear another pane's real error. It is `any`
// because both *Stream and *Artifact publish; each claims only its own.
type publishedMsg struct {
	owner any
	err   error
}

// ownedBy reports whether the publish result belongs to s: tagged by it, or
// untagged (nil owner — test-synthesized; a real publish always tags).
func (m publishedMsg) ownedBy(s any) bool {
	return m.owner == nil || m.owner == s
}

// renderFrame turns one received frame into one or more stream lines: a
// fixed-width author column hued by the author's role, then the chat.message text
// soft-wrapped to the remaining content width. The first line carries the author
// label + the first wrapped segment; continuation lines indent past the author
// column (no repeated label) in the same author hue, so a long message reads as a
// single paragraph aligned under its author rather than clipping at the right
// edge. A non-chat record renders its kind compactly so nothing is silently
// dropped.
func (s *Stream) renderFrame(m sextant.Message) []string {
	author, hue := s.authorLabel(m.Frame.Author)
	authorCol := lipgloss.NewStyle().
		Foreground(hue).
		Width(authorWidth).
		MaxWidth(authorWidth).
		Render(author)

	cm, ok := parseChatMessage(m.Frame.Record)
	var text string
	var bodyStyle lipgloss.Style
	if !ok {
		// Unknown record: show its kind so the line is honest, not blank.
		kind := m.Frame.Kind
		if kind == "" {
			kind = "record"
		}
		text = "(" + kind + ")"
		bodyStyle = lipgloss.NewStyle().Foreground(s.theme.Dim)
	} else {
		text = cm.Text
		bodyStyle = lipgloss.NewStyle().Foreground(hue)
	}

	// The text body starts one space after the author column; continuation lines
	// indent to the same offset. Wrap to whatever width remains (a sane floor when
	// the pane is too narrow to leave room — the widget then truncates).
	indent := authorWidth + 1
	bodyW := s.w - indent
	if bodyW < minBodyWidth {
		bodyW = minBodyWidth
	}
	segments := wrapText(text, bodyW)
	if len(segments) == 0 {
		segments = []string{""}
	}

	lines := make([]string, 0, len(segments))
	pad := strings.Repeat(" ", indent)
	for i, seg := range segments {
		body := bodyStyle.Render(seg)
		if i == 0 {
			lines = append(lines, authorCol+" "+body)
		} else {
			lines = append(lines, pad+body)
		}
	}
	return lines
}

// minBodyWidth is the floor the message body wraps to when the pane is too narrow
// to leave room past the author column. At or below it the widget's own truncate
// is the safety net; wrapping never produces zero-width segments.
const minBodyWidth = 8

// wrapText soft-wraps s to width, breaking on word boundaries and hard-breaking a
// single token longer than width (so a long unbroken string — a URL, a hash —
// still folds rather than running off the edge). It splits on existing newlines
// first so an embedded newline stays a line break. A width below 1 collapses to 1.
func wrapText(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		wrapped := wordwrap.String(para, width)
		// wordwrap leaves words longer than width intact, so hard-break any line
		// that still exceeds width into width-cell chunks.
		for _, line := range strings.Split(wrapped, "\n") {
			out = append(out, hardBreak(line, width)...)
		}
	}
	return out
}

// hardBreak splits a single line into chunks no wider than width, by display
// cells. It walks grapheme clusters, not runes, so a multi-rune glyph — a ZWJ
// emoji sequence, a base letter plus combining marks — moves to the next chunk
// whole rather than being split mid-cluster (a per-rune walk would also
// miscount a joined sequence's width by summing its parts). For plain ASCII
// every rune is its own single-cell cluster, so the chunks are exactly what the
// rune walk produced. A line already within width is returned unchanged.
func hardBreak(line string, width int) []string {
	if lipgloss.Width(line) <= width {
		return []string{line}
	}
	var out []string
	var cur strings.Builder
	curW := 0
	g := uniseg.NewGraphemes(line)
	for g.Next() {
		gw := g.Width()
		if curW+gw > width && curW > 0 {
			out = append(out, cur.String())
			cur.Reset()
			curW = 0
		}
		cur.WriteString(g.Str())
		curW += gw
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// authorLabel resolves a frame author id to a display label and a role hue. With
// an authors map it shows the display name in the author's role hue; without an
// entry it shows a short author (the id's tail) in the default foreground — the
// documented fallback when no presence map is wired in. A frame carries only the
// author id; name + role resolution is the dash's job (it builds the map from
// presence), never the surface's.
func (s *Stream) authorLabel(id string) (string, lipgloss.Color) {
	if id == "" {
		return "—", s.theme.Fg
	}
	if a, ok := s.authors[id]; ok && a.Name != "" {
		return shortLabel(a.Name), s.theme.RoleHue(a.Role)
	}
	return shortLabel(id), s.theme.Fg
}

// shortLabel clamps a label to the author column, taking the trailing characters
// of a long id (ULIDs share a prefix; the tail is the distinguishing part).
func shortLabel(s string) string {
	if len(s) <= authorWidth-1 {
		return s
	}
	return "…" + s[len(s)-(authorWidth-2):]
}

// dropMarker renders a coalesced gap marker for N dropped events, so an overflow
// shows as one honest line in the stream rather than vanishing.
func (s *Stream) dropMarker(n int) string {
	marker := fmt.Sprintf("⋯ %d message(s) dropped (buffer overflow) ⋯", n)
	return lipgloss.NewStyle().Foreground(s.theme.StatusHue(theme.StatusDraining)).Render(marker)
}

// trimMarker renders the truncation marker for n messages dropped from the
// front of the bounded entry log, mirroring dropMarker's convention, so the
// capped buffer reads as an honest gap at the top rather than silently missing
// history.
func (s *Stream) trimMarker(n int) string {
	marker := fmt.Sprintf("⋯ older history trimmed (%d message(s)) ⋯", n)
	return lipgloss.NewStyle().Foreground(s.theme.StatusHue(theme.StatusDraining)).Render(marker)
}

// Stop tears the feed down, ending its blocked Next pump (the Surface contract's
// teardown). The layout calls it when unmounting the surface; a standalone host
// calls it on quit. It is safe to call more than once.
func (s *Stream) Stop() { s.feed.Stop() }
