package chat

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/tui/component"
)

// Mode is the modal state of the chat TUI. Spec §"MVP (Iteration 4 —
// Modal)". The default is NORMAL; INSERT is unreachable in read mode.
type Mode int

const (
	ModeNormal Mode = iota
	ModeInsert
)

// FocusArea identifies which surface is currently "selected" in
// NORMAL mode: either a turn in the stream, or the composer below
// it. Default at startup is FocusComposer — chat opens compose-first.
//
// Renamed from `Focus` to free that name for the
// `component.Component.Focus() tea.Cmd` method.
type FocusArea int

const (
	FocusStream FocusArea = iota
	FocusComposer
)

// Options configure a chat Model. AgentName/Branch are header chrome;
// Read disables INSERT (and hides the composer in view.go).
// AgentID is used to populate RestartRequestedMsg when the operator
// presses R in the lost state.
type Options struct {
	AgentName string
	AgentID   uuid.UUID
	Branch    string
	Read      bool
}

// RestartRequestedMsg is emitted when the operator presses R while the
// chat believes the agent is lost. program.go translates this into a
// restart_agent RPC.
type RestartRequestedMsg struct {
	AgentID uuid.UUID
}

// SendFunc is the callback the model invokes when the operator hits
// Enter in INSERT. The receiver is responsible for dispatching the
// prompt_agent RPC; program.go wires this against pkg/client in T10.
type SendFunc func(text string)

// RestartFunc is the callback the host invokes when a RestartRequestedMsg
// is received. program.go wires this against the Bus.RestartAgent RPC.
// Errors are swallowed — the watcher publishes "restarted" and the model
// re-enables input automatically.
type RestartFunc func(agentID uuid.UUID)

// Model is the bubbletea reducer state. Use New to construct, then
// WithTurns to seed any pre-existing transcript before passing to
// tea.NewProgram.
//
// Model satisfies `component.Component`: it owns content-area
// rendering, exposes SetSize / Focus / Blur / Focused, and emits
// intent messages (`component.DoneMsg`) instead of `tea.Quit`. The
// surrounding chrome (header, status bar) is the host's
// responsibility — see `standalone.go` for the standalone wrapper.
type Model struct {
	opts           Options
	mode           Mode
	focus          FocusArea
	savedFocus     FocusArea // snapshot of focus when entering INSERT
	savedSelection int       // snapshot of selection when entering INSERT
	turns          []Turn
	selection      int
	gPending       bool // first 'g' of 'gg' seen, waiting for the second
	width          int
	height         int
	streamHeight   int // computed in Update(WindowSizeMsg); rows available inside the stream box
	styles         Styles
	keys           keyMap
	composer       textarea.Model
	send           SendFunc
	restart        RestartFunc
	componentFocus bool // tracks the Component.Focused() bit, independent of intra-component focus

	// lastLifecycle is the most recent lifecycle envelope received via
	// lifecycleMsg. Used by the host's renderHeader to draw the status
	// dot (feat-chat-tui-status-dot). Zero value when no envelope has
	// been seen yet — host renders a muted dot in that case.
	lastLifecycle sextantproto.LifecyclePayload
	hasLifecycle  bool
}

// New returns a *Model with default styles/keys, mode=NORMAL,
// focus area = FocusComposer (compose-first), selection=0.
// In Read mode focus area starts on FocusStream (no composer).
//
// Returns a pointer because Model implements
// `component.Component` on *Model (the SetSize/Focus/Blur/Focused
// methods need to mutate state). All chainable mutators (WithTurns,
// WithSendHook) also return *Model so the fluent construction
// pattern still works: `m := chat.New(opts).WithTurns(t)`.
//
// The Component-level focus bit defaults to false; a standalone
// wrapper calls Focus() on construction. The dash sets it explicitly
// when the pane becomes active.
func New(opts Options) *Model {
	ta := textarea.New()
	ta.Placeholder = "press i to compose…"
	ta.CharLimit = 0
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.Prompt = "▎ "
	ta.Blur()
	focus := FocusComposer
	if opts.Read {
		focus = FocusStream
	}
	return &Model{
		opts:     opts,
		mode:     ModeNormal,
		focus:    focus,
		styles:   defaultStyles(),
		keys:     defaultKeys(),
		composer: ta,
	}
}

// WithSendHook installs the callback invoked on INSERT-Enter. Returns
// the model (pointer) so callers can chain it with WithTurns.
func (m *Model) WithSendHook(fn SendFunc) *Model {
	m.send = fn
	return m
}

// WithRestartHook installs the callback invoked when a
// RestartRequestedMsg is handled by the host. program.go wires this
// against Bus.RestartAgent.
func (m *Model) WithRestartHook(fn RestartFunc) *Model {
	m.restart = fn
	return m
}

// WithTurns seeds the transcript. Selection is set to the last turn
// index (used when focus=FocusStream), but focus itself is left as-is
// (FocusComposer by default from New, so callers open compose-first).
// Returns *Model so the fluent chain works.
func (m *Model) WithTurns(turns []Turn) *Model {
	m.turns = turns
	if len(turns) == 0 {
		m.selection = 0
	} else {
		m.selection = len(turns) - 1
	}
	// focus is left as-is (FocusComposer by default from New).
	return m
}

func (m *Model) Mode() Mode           { return m.mode }
func (m *Model) FocusArea() FocusArea { return m.focus }
func (m *Model) Selection() int       { return m.selection }
func (m *Model) Turns() []Turn        { return m.turns }
func (m *Model) IsRead() bool         { return m.opts.Read }

// inputDisabled reports whether the chat should reject prompt input.
// True only while we believe the agent is `lost` and waiting for a
// post-restart `started` envelope.
func (m *Model) inputDisabled() bool {
	if !m.hasLifecycle {
		return false
	}
	return m.lastLifecycle.Transition == sextantproto.LifecycleLostEvent
}

// Draft returns the current composer text. Exposed for tests.
func (m *Model) Draft() string { return m.composer.Value() }

// Init returns no startup commands — frame subscription is wired by
// program.go and seeded via WithSubscription (Task 8).
func (m *Model) Init() tea.Cmd { return nil }

// Update is the reducer. Mode-aware dispatch: in NORMAL we handle vim-
// flavored navigation; INSERT is handled by updateInsert.
//
// Window-size handling lives in SetSize, not here. The host (standalone
// wrapper or dash) calls SetSize with the content rect after
// subtracting its own chrome. Tests that need a specific size should
// call SetSize directly. WindowSizeMsg is still accepted here as a
// no-op so message routing from the host doesn't trip on it.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Host has already called SetSize with the content rect;
		// nothing for the reducer to do.
		_ = msg
		return m, nil
	case frameMsg:
		// at-bottom: either on the last turn in FocusStream, or focused on
		// the composer (which implies "not navigating; keep me caught up").
		atBottom := (m.focus == FocusStream && (m.selection == len(m.turns)-1 || len(m.turns) == 0)) ||
			m.focus == FocusComposer
		// Synthesize a one-frame slice and feed it through the same
		// collapser used at seed time. We append the produced turn(s)
		// rather than rebuilding from scratch so existing turn objects
		// (with their tool-call indices) stay stable.
		newTurns := FramesToTurns(append(framesFromTurns(m.turns), msg.Frame))
		grew := len(newTurns) > len(m.turns)
		m.turns = newTurns
		if grew && atBottom {
			m.selection = len(m.turns) - 1
		}
		if m.selection > len(m.turns)-1 && len(m.turns) > 0 {
			m.selection = len(m.turns) - 1
		}
		return m, nil
	case lifecycleMsg:
		// Store the most recent envelope so the host's header can
		// render a status dot (feat-chat-tui-status-dot). --tail close
		// lives in program.go.
		m.lastLifecycle = msg.Payload
		m.hasLifecycle = true
		return m, nil
	case subscriptionEndedMsg:
		// Upstream channel closed — usually the daemon went away or the
		// operator hit Ctrl-C. Emit DoneMsg; the host (standalone wrapper
		// or dash) decides whether to quit or close just this pane.
		return m, emitDone
	case tea.KeyMsg:
		if m.mode == ModeInsert {
			return m.updateInsert(msg)
		}
		return m.updateNormal(msg)
	}
	return m, nil
}

func (m *Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 'gg' is two-key; clear the pending flag on anything else.
	gPending := m.gPending
	m.gPending = false

	// R restarts the agent — only meaningful (and only handled) when lost.
	if m.inputDisabled() && key.Matches(msg, m.keys.NormalRestart) {
		return m, func() tea.Msg {
			return RestartRequestedMsg{AgentID: m.opts.AgentID}
		}
	}

	switch {
	case key.Matches(msg, m.keys.NormalQuit):
		// Don't call tea.Quit directly — emit a DoneMsg intent so the
		// host can route it (standalone → tea.Quit, dash → close pane).
		return m, emitDone
	case key.Matches(msg, m.keys.NormalDown):
		if m.focus == FocusStream {
			if m.selection < len(m.turns)-1 {
				m.selection++
			} else if !m.opts.Read {
				// Step past last turn → composer focus (NORMAL mode only;
				// Read mode has no composer to focus on).
				m.focus = FocusComposer
			}
		}
		// j from FocusComposer is a no-op (composer is the bottom stop).
	case key.Matches(msg, m.keys.NormalUp):
		if m.focus == FocusComposer {
			m.focus = FocusStream
			if n := len(m.turns); n > 0 {
				m.selection = n - 1
			}
		} else if m.selection > 0 {
			m.selection--
		}
	case key.Matches(msg, m.keys.NormalBottom):
		m.focus = FocusStream
		if n := len(m.turns); n > 0 {
			m.selection = n - 1
		}
	case key.Matches(msg, m.keys.NormalTop):
		if gPending {
			m.focus = FocusStream
			m.selection = 0
		} else {
			m.gPending = true
		}
	case !m.opts.Read && !m.inputDisabled() && key.Matches(msg, m.keys.NormalInsert):
		// Snapshot the pre-INSERT focus + selection so Esc can restore it.
		m.savedFocus = m.focus
		m.savedSelection = m.selection
		m.mode = ModeInsert
		m.composer.Focus()
		return m, textarea.Blink
	}
	return m, nil
}

// updateInsert handles keystrokes when the operator is in INSERT. Esc
// returns to NORMAL preserving the draft. Enter dispatches the send hook,
// locally echoes the user turn, clears the composer, and bounces back to
// NORMAL with selection on the new last turn (empty draft is a no-op).
// All other keys are forwarded to the textarea so typing works as expected.
// framesFromTurns reconstructs an approximate Frame slice from a turn
// slice for the incremental-append path in Update(frameMsg). The shape
// is lossy (Body maps aren't preserved verbatim) but FramesToTurns only
// needs Actor/Text/FrameKind/ToolName/Ts to reconstitute the same turn
// structure for already-rendered rows.
func framesFromTurns(turns []Turn) []Frame {
	var frames []Frame
	for _, t := range turns {
		switch t.Actor {
		case ActorUser:
			frames = append(frames, Frame{Ts: t.Ts, Actor: ActorUser, Text: t.Text})
		case ActorAgent:
			frames = append(frames, Frame{Ts: t.Ts, FrameKind: sextantproto.FrameAssistantText, Body: map[string]any{"text": t.Text}})
			for _, tc := range t.ToolCalls {
				frames = append(frames, Frame{
					Ts: tc.StartTs, FrameKind: sextantproto.FrameToolCall,
					ToolName: tc.Name, Body: map[string]any{"path": tc.Arg},
				})
				if tc.Status != ToolStatusPending {
					body := map[string]any{}
					if tc.Status == ToolStatusFailed {
						body["error"] = "boom"
					}
					frames = append(frames, Frame{
						Ts: tc.StartTs.Add(tc.Duration), FrameKind: sextantproto.FrameToolResult,
						ToolName: tc.Name, Body: body,
					})
				}
			}
		case ActorSystem:
			frames = append(frames, Frame{Ts: t.Ts, FrameKind: sextantproto.FrameSystemNote, Body: map[string]any{"text": t.Text}})
		}
	}
	return frames
}

// emitDone is the shared tea.Cmd for surfacing a DoneMsg intent.
// Defined as a package-level value so the chat reducer doesn't
// allocate a closure on every emission.
func emitDone() tea.Msg { return component.DoneMsg{} }

// SetSize implements component.Component. Stores the content rect
// the host gave us and recomputes streamHeight (rows available
// inside the stream box). The host is expected to have already
// subtracted its own chrome (header, status bar) from the height —
// what we receive is the content rect.
//
// Reserves rows inside the content rect for the stream box's two
// border lines and, in non-read mode, the composer box's three
// rows plus a blank gap above the host's status bar. The standalone
// wrapper reserves the matching outer-chrome rows so the math lines
// up with the legacy renderer.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
	// 2 — stream box top + bottom border
	// 3 — composer box (top + content + bottom)
	// ----
	//   5 reserved inside the content rect for NORMAL/INSERT mode.
	//   In READ mode the composer is hidden — only the 2 stream-box
	//   border rows are reserved.
	reserved := 5
	if m.opts.Read {
		reserved = 2
	}
	m.streamHeight = h - reserved
	if m.streamHeight < 1 {
		m.streamHeight = 1
	}
	m.composer.SetWidth(w)
}

// Focus implements component.Component. Marks the component as the
// active surface. Returns no cmd — the chat reducer keeps cursor
// blinking inside the textarea, which has its own focus separate
// from the component-level bit.
func (m *Model) Focus() tea.Cmd {
	m.componentFocus = true
	return nil
}

// Blur implements component.Component. Parks the component. Does
// not touch the textarea's internal focus state — that's driven by
// the NORMAL ↔ INSERT mode transition inside Update, not by the
// component-level focus bit.
func (m *Model) Blur() { m.componentFocus = false }

// Focused implements component.Component.
func (m *Model) Focused() bool { return m.componentFocus }

// ShortHelp implements component.Component. Returns the most useful
// bindings for the current mode, suitable for one row of a help bar.
// Read mode hides INSERT-only bindings.
func (m *Model) ShortHelp() []key.Binding {
	if m.opts.Read {
		return []key.Binding{m.keys.NormalDown, m.keys.NormalUp, m.keys.NormalQuit}
	}
	if m.mode == ModeInsert {
		return []key.Binding{m.keys.InsertSend, m.keys.InsertExit}
	}
	return []key.Binding{
		m.keys.NormalDown, m.keys.NormalUp,
		m.keys.NormalInsert, m.keys.NormalQuit,
	}
}

// FullHelp implements component.Component. Returns the full key
// vocabulary grouped by topic: navigation, mode, control. Bubble
// `help` renders one column per group.
func (m *Model) FullHelp() [][]key.Binding {
	nav := []key.Binding{
		m.keys.NormalDown, m.keys.NormalUp,
		m.keys.NormalTop, m.keys.NormalBottom,
	}
	control := []key.Binding{m.keys.NormalQuit}
	if m.opts.Read {
		return [][]key.Binding{nav, control}
	}
	mode := []key.Binding{
		m.keys.NormalInsert, m.keys.InsertExit,
		m.keys.InsertSend, m.keys.InsertNewline,
	}
	return [][]key.Binding{nav, mode, control}
}

func (m *Model) updateInsert(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.InsertExit):
		// Restore the focus + selection snapshot taken when 'i' was pressed.
		m.focus = m.savedFocus
		m.selection = m.savedSelection
		m.mode = ModeNormal
		m.composer.Blur()
		return m, nil
	case key.Matches(msg, m.keys.InsertSend):
		// Defense in depth: insert shouldn't be reachable when disabled,
		// but guard here too so no prompts go through if it somehow is.
		if m.inputDisabled() {
			return m, nil
		}
		text := strings.TrimSpace(m.composer.Value())
		if text == "" {
			// Empty draft: no-op. Stay in INSERT — the operator likely
			// hit Enter by accident or hasn't typed yet.
			return m, nil
		}
		if m.send != nil {
			m.send(text)
		}
		m.composer.SetValue("")
		// Local echo: surface the operator's prompt as a user turn so
		// the conversation reads naturally before the daemon's frame
		// round-trips back. Send always lands on FocusStream at the new
		// last turn — the operator sees their just-sent message highlighted.
		m.turns = append(m.turns, Turn{Ts: time.Now(), Actor: ActorUser, Text: text})
		m.focus = FocusStream
		m.selection = len(m.turns) - 1
		m.mode = ModeNormal
		m.composer.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	return m, cmd
}
