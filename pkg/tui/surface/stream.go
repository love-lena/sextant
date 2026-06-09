package surface

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"

	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/tui/busfeed"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// authorWidth is the fixed column the author label occupies in a rendered line,
// so the text bodies align down the stream regardless of name length.
const authorWidth = 14

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
// config (participate), where stepping in lets the operator type a line and Enter
// publishes it. Without it the surface is "tail" (observe only) — the same one
// read-stream, no send side (ADR-0023).
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
// events without a bus. Esc out of an active compose emits a DoneMsg.
type Stream struct {
	client  *sextant.Client
	ctx     context.Context
	subject string
	theme   theme.Theme
	keys    theme.Keymap

	feed    *busfeed.Feed
	stream  widget.Stream
	input   textinput.Model
	compose bool
	authors map[string]Author

	// entries is the ordered log of what the stream has shown — each a received
	// frame or a coalesced drop-gap — kept so SetTheme can re-render the buffer
	// with the new palette (a rendered line bakes in the author hue, so a runtime
	// theme switch must replay the log to re-hue it).
	entries []streamEntry

	focus widget.Focus
	// w, h is the inner area; the compose row, when shown, takes the last line.
	w, h int
	err  error
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
	in := textinput.New()
	in.Prompt = "> "
	in.Placeholder = "message…"
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

// SetSize sizes the inner area, reserving the bottom row for the compose line
// when compose is on and another for the error footer when an error is showing.
// A width change reflows the buffer: message lines soft-wrap to the content
// width (renderEntry), so a narrow/wide resize must re-wrap every logged entry.
func (s *Stream) SetSize(w, h int) {
	widthChanged := w != s.w
	s.w, s.h = w, h
	if w > 0 {
		s.input.Width = w - lipgloss.Width(s.input.Prompt) - 1
		if s.input.Width < 1 {
			s.input.Width = 1
		}
	}
	if widthChanged {
		s.replay()
	}
	s.relayout()
}

// relayout sizes the stream viewport to the inner area minus the compose row (if
// any) and the error-footer row (if an error is showing), so neither overlaps the
// stream.
func (s *Stream) relayout() {
	streamH := s.h
	if s.compose {
		streamH--
	}
	if s.err != nil {
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
	s.replay()
}

// replay re-renders the whole entry log to stream lines for the current theme and
// width. It is called when the palette OR the inner width changes (a theme switch,
// a reflow), since both the per-line hues and the soft-wrap depend on the current
// state: a wrapped line wraps to the current content width, and a narrower width
// must reflow the buffer to fewer columns.
func (s *Stream) replay() {
	var lines []string
	for _, e := range s.entries {
		lines = append(lines, s.renderEntry(e)...)
	}
	s.stream.SetLines(lines)
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

// SetFocus sets the three-state focus. Stepping in (active) focuses the compose
// input when compose is on; stepping out blurs it.
func (s *Stream) SetFocus(f widget.Focus) {
	s.focus = f
	if !s.compose {
		return
	}
	if f == widget.FocusActive {
		s.input.Focus()
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

// Init opens the feed. The pump runs from Update: every EventMsg and DroppedMsg
// re-issues Next. A nil client (a seeded gallery / golden) skips the subscribe —
// those feed events directly, the same convention the artifact surface follows.
func (s *Stream) Init() tea.Cmd {
	if s.client == nil {
		return nil
	}
	return s.feed.Subscribe(s.ctx)
}

// Update drives the feed pump, renders incoming frames, and — when active and
// composing — handles typing, Enter (publish), and Esc (step out → DoneMsg).
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
		e := streamEntry{frame: msg.Message, hasFrame: true}
		s.entries = append(s.entries, e)
		s.stream.Append(s.renderEntry(e)...)
		return s.feed.Next() // keep pumping
	case busfeed.DroppedMsg:
		e := streamEntry{dropped: msg.N}
		s.entries = append(s.entries, e)
		s.stream.Append(s.renderEntry(e)...)
		return s.feed.Next() // DroppedMsg is not terminal; keep pumping
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

// handleKey routes a key while the surface is active: scrolling always, plus
// compose when enabled. The bindings come from the keymap (keys are data), not
// literal strings, so an operator's rebind is honoured here as it is in the
// chrome and the inner widget. Back steps out (DoneMsg); Enter publishes the
// composed line; the scroll bindings review the backlog; other keys edit the
// compose buffer.
func (s *Stream) handleKey(msg tea.KeyMsg) tea.Cmd {
	if s.focus != widget.FocusActive {
		return nil
	}
	switch {
	case key.Matches(msg, s.keys.Back):
		s.input.SetValue("")
		s.input.Blur()
		return doneCmd(s.ID())
	case key.Matches(msg, s.keys.Enter):
		if !s.compose {
			return nil
		}
		text := strings.TrimSpace(s.input.Value())
		if text == "" {
			return nil
		}
		s.input.SetValue("")
		return s.publish(text)
	case key.Matches(msg, s.keys.Up), key.Matches(msg, s.keys.Down):
		// Scrolling the stream takes precedence over compose history (which the
		// textinput does not implement anyway), so up/down review the backlog.
		s.stream, _ = s.stream.Update(msg)
		return nil
	}
	if s.compose {
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(msg)
		return cmd
	}
	return nil
}

// View renders the stream, the compose line below it when compose is on, and an
// error footer below that when a subscribe or publish failed — kept visible
// rather than swallowed (fail-loud).
func (s *Stream) View() string {
	parts := []string{s.stream.View(s.theme, s.focus)}
	if s.compose {
		parts = append(parts, s.composeLine())
	}
	if s.err != nil {
		parts = append(parts, errorFooter(s.theme, s.err, s.w))
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// composeLine renders the bottom compose row. When active it shows the live
// textinput; when not, a dim hint that Enter steps in to type.
func (s *Stream) composeLine() string {
	if s.focus == widget.FocusActive {
		return s.input.View()
	}
	hint := "enter to compose"
	w := s.w
	if w <= 0 {
		w = 1
	}
	return lipgloss.NewStyle().Foreground(s.theme.Dim).Width(w).MaxWidth(w).Render("> " + hint)
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
// cells (so multi-cell runes are not split mid-glyph). A line already within
// width is returned unchanged.
func hardBreak(line string, width int) []string {
	if lipgloss.Width(line) <= width {
		return []string{line}
	}
	var out []string
	var cur strings.Builder
	curW := 0
	for _, r := range line {
		rw := lipgloss.Width(string(r))
		if curW+rw > width && curW > 0 {
			out = append(out, cur.String())
			cur.Reset()
			curW = 0
		}
		cur.WriteRune(r)
		curW += rw
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

// Stop tears the feed down, ending its blocked Next pump (the Surface contract's
// teardown). The layout calls it when unmounting the surface; a standalone host
// calls it on quit. It is safe to call more than once.
func (s *Stream) Stop() { s.feed.Stop() }
