// model.go — Bubble Tea model for the agents list Component.
//
// Adapted from the original cmd/sextant-tui-agents/model.go so the
// same surface can run standalone *or* mount as a pane in the dash:
//
//   - Quit emits component.DoneMsg instead of tea.Quit (the host
//     decides whether DoneMsg means "exit the program" or "close this
//     pane").
//   - Window-size handling moves to SetSize, called by the host after
//     subtracting its own chrome. WindowSizeMsg is forwarded but
//     ignored.
//   - Focus / Blur / Focused track whether the Component is the
//     interactive surface. A standalone host calls Focus() once at
//     startup; the dash drives focus per pane.
//   - ShortHelp / FullHelp expose the keymap to the host's help-bar
//     renderer.
package agents

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/tui/component"
)

// uiStateBucket and selectedAgentField mirror conventions/tui-conventions.md
// §"ui.state.* keys". One bucket holds every operator's keys; the wire
// key shape is `<operator>.<field>` (the `ui.state` prefix is implicit
// in the bucket).
const (
	UIStateBucket      = "ui_state"
	SelectedAgentField = "selected_agent"
	NoneSelection      = "none"
)

// Bus is the Client-shape subset the Component depends on. Defined as
// an interface so tests can drive the model without touching NATS.
// The shape mirrors pkg/client method signatures so a *client.Client
// satisfies it directly.
type Bus interface {
	RPC(ctx context.Context, verb string, req, resp any, opts ...client.RPCOption) error
	Subscribe(ctx context.Context, subject string, opts ...client.SubscribeOption) (<-chan client.Message, error)
	WatchKV(ctx context.Context, bucket, key string) (<-chan client.KVUpdate, error)
	PutKV(ctx context.Context, bucket, key string, value []byte) error
}

// Options configure a Model. Operator is the identity used for the
// ui_state KV key. SelectedID, when non-empty, seeds the highlighted
// row at startup — used by `sextant agents show <id> -i` so the TUI
// opens with `<id>` already focused.
type Options struct {
	Bus        Bus
	Operator   string
	SelectedID string
}

// Model is the Bubble Tea model. Implements component.Component so it
// can run standalone (under component.Host) or mounted as a pane in
// `sextant dash`.
type Model struct {
	bus      Bus
	operator string
	th       localTheme
	keys     keyMap

	agents    []sextantproto.AgentSummary
	cursor    int
	selected  string // selected agent UUID per ui_state KV; "" or "none" = no selection
	pending   int
	refreshed time.Time
	helpOpen  bool
	errMsg    string
	width     int
	height    int

	focused bool

	// initialSelectedID is honored after the first list_agents RPC
	// completes so the cursor lands on the requested row.
	initialSelectedID string
}

// keyMap collects the component's key bindings. Exported via
// ShortHelp / FullHelp so the host's help bar can render them.
type keyMap struct {
	Up      key.Binding
	Down    key.Binding
	Top     key.Binding
	Bottom  key.Binding
	Refresh key.Binding
	Select  key.Binding
	Help    key.Binding
	Quit    key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:      key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k/↑", "up")),
		Down:    key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j/↓", "down")),
		Top:     key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "top")),
		Bottom:  key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "bottom")),
		Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Select:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("⏎", "select")),
		Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

// New constructs an empty Model.
func New(opts Options) *Model {
	return &Model{
		bus:               opts.Bus,
		operator:          opts.Operator,
		th:                defaultTheme(),
		keys:              defaultKeys(),
		initialSelectedID: opts.SelectedID,
	}
}

// --- messages ------------------------------------------------------------

// agentsLoadedMsg lands when list_agents RPC completes.
type agentsLoadedMsg struct {
	agents []sextantproto.AgentSummary
	at     time.Time
	err    error
}

// lifecycleMsg lands when an `agents.*.lifecycle` envelope arrives; it
// triggers a re-fetch of the agent list.
type lifecycleMsg struct{}

// pendingDeltaMsg lands when a `user_input.requests.>` envelope arrives.
type pendingDeltaMsg struct{ delta int }

// selectedAgentMsg lands when the `ui_state` KV key changes (possibly
// written by us, possibly by a sibling TUI).
type selectedAgentMsg struct{ value string }

// kvPutDoneMsg lands when our own PutKV completes; surfaces errors.
type kvPutDoneMsg struct{ err error }

// --- Component interface -------------------------------------------------

// SetSize implements component.Component.
func (m *Model) SetSize(w, h int) {
	m.width, m.height = w, h
}

// Focus implements component.Component. The Bubble Tea reducer doesn't
// have a per-component cursor blink, so Focus returns nil.
func (m *Model) Focus() tea.Cmd {
	m.focused = true
	return nil
}

// Blur implements component.Component.
func (m *Model) Blur() { m.focused = false }

// Focused implements component.Component.
func (m *Model) Focused() bool { return m.focused }

// ShortHelp implements component.Component. Surfaces the most useful
// bindings in one row (per bubbles/help convention).
func (m *Model) ShortHelp() []key.Binding {
	return []key.Binding{
		m.keys.Up,
		m.keys.Down,
		m.keys.Select,
		m.keys.Refresh,
		m.keys.Help,
		m.keys.Quit,
	}
}

// FullHelp implements component.Component. Grouped grid for the help
// overlay.
func (m *Model) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{m.keys.Up, m.keys.Down, m.keys.Top, m.keys.Bottom},
		{m.keys.Select, m.keys.Refresh},
		{m.keys.Help, m.keys.Quit},
	}
}

// --- Init ----------------------------------------------------------------

// Init returns the commands the program runs at startup: list_agents,
// the three subscriptions, and the KV watch.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.fetchAgentsCmd(),
		m.subscribeLifecycleCmd(),
		m.subscribePendingCmd(),
		m.watchSelectedAgentCmd(),
	)
}

// --- Update --------------------------------------------------------------

// Update is the Bubble Tea reducer.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Host has called SetSize already with the content rect; nothing
		// to do here. Kept as a no-op case so explicit forwarding doesn't
		// surprise.
		_ = msg
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case component.LoadMsg:
		// LoadMsg seeds the focused row on the next list_agents result —
		// the dash uses this when routing OpenMsg between panes.
		m.initialSelectedID = msg.ID
		return m, nil
	case agentsLoadedMsg:
		if msg.err != nil {
			m.errMsg = fmt.Sprintf("list_agents: %v", msg.err)
			return m, nil
		}
		m.errMsg = ""
		m.agents = msg.agents
		m.refreshed = msg.at
		if m.cursor >= len(m.agents) {
			m.cursor = max0(len(m.agents) - 1)
		}
		// Honor any pending initial-select.
		if m.initialSelectedID != "" {
			for i, a := range m.agents {
				if a.UUID.String() == m.initialSelectedID {
					m.cursor = i
					break
				}
			}
			m.initialSelectedID = ""
		}
		return m, nil
	case lifecycleMsg:
		return m, m.fetchAgentsCmd()
	case pendingDeltaMsg:
		m.pending += msg.delta
		if m.pending < 0 {
			m.pending = 0
		}
		return m, nil
	case selectedAgentMsg:
		m.selected = msg.value
		return m, nil
	case kvPutDoneMsg:
		if msg.err != nil {
			m.errMsg = fmt.Sprintf("put selected_agent: %v", msg.err)
		}
		return m, nil
	}
	return m, nil
}

// emitDone is the package-level cmd that emits a DoneMsg intent.
// Hosts translate this to tea.Quit (standalone) or "close pane" (dash).
func emitDone() tea.Msg { return component.DoneMsg{} }

// handleKey dispatches the keymap.
func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.helpOpen {
		switch msg.String() {
		case "?", "esc", "q", "ctrl+c":
			m.helpOpen = false
		}
		return m, nil
	}
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, emitDone
	case key.Matches(msg, m.keys.Help):
		m.helpOpen = true
	case msg.String() == "esc":
		m.errMsg = ""
	case key.Matches(msg, m.keys.Down):
		if m.cursor < len(m.agents)-1 {
			m.cursor++
		}
	case key.Matches(msg, m.keys.Up):
		if m.cursor > 0 {
			m.cursor--
		}
	case key.Matches(msg, m.keys.Top):
		m.cursor = 0
	case key.Matches(msg, m.keys.Bottom):
		m.cursor = max0(len(m.agents) - 1)
	case key.Matches(msg, m.keys.Refresh):
		return m, m.fetchAgentsCmd()
	case key.Matches(msg, m.keys.Select):
		if len(m.agents) > 0 {
			return m, m.putSelectedAgentCmd(m.agents[m.cursor].UUID.String())
		}
	}
	return m, nil
}

// --- View ----------------------------------------------------------------

// View renders the component's content area. The surrounding chrome
// (title, status bar) is the host's responsibility — the standalone
// wrapper in standalone.go installs a ChromeFunc that draws what the
// pre-Component model used to render inline.
func (m *Model) View() string {
	var b strings.Builder
	if m.errMsg != "" {
		b.WriteString(m.th.errorBar.Render("! " + m.errMsg))
		b.WriteString("  ")
		b.WriteString(m.th.muted.Render("(Esc to dismiss)"))
		b.WriteString("\n")
	}
	b.WriteString(m.renderTable())
	if m.helpOpen {
		b.WriteString("\n\n")
		b.WriteString(m.renderHelp())
	}
	return b.String()
}

func (m *Model) renderTable() string {
	header := fmt.Sprintf("%-24s  %-8s  %-16s  %s",
		"NAME", "UUID", "TEMPLATE", "LIFECYCLE")
	var rows []string
	rows = append(rows, m.th.header.Render(header))
	if len(m.agents) == 0 {
		rows = append(rows, m.th.muted.Render("  (no agents yet — create one with `sextant agents create`)"))
		return strings.Join(rows, "\n")
	}
	for i, a := range m.agents {
		row := fmt.Sprintf("%-24s  %-8s  %-16s  %s",
			truncate(a.Name, 24),
			shortUUID(a.UUID),
			truncate(a.Template, 16),
			truncate(a.Lifecycle, 12))
		switch {
		case i == m.cursor:
			rows = append(rows, m.th.rowActive.Render(row))
		case m.selected != "" && m.selected != NoneSelection && a.UUID.String() == m.selected:
			// Sibling-TUI selection — mark with a leading bullet without
			// reversing the row.
			rows = append(rows, m.th.row.Render("> "+row))
		default:
			rows = append(rows, m.th.row.Render("  "+row))
		}
	}
	return strings.Join(rows, "\n")
}

// AgentsCount returns the current agent count. Exposed so the
// standalone wrapper's status bar can show "agents=N".
func (m *Model) AgentsCount() int { return len(m.agents) }

// SelectedSummary returns the currently-published selected_agent UUID
// (per the ui_state KV). Returns the empty string when nothing is
// selected. Exposed so the standalone wrapper's status bar can show
// "selected=<abbr>".
func (m *Model) SelectedSummary() string { return m.selected }

// Pending returns the pending-input count. Exposed so the standalone
// wrapper's status bar can show "pending=N".
func (m *Model) Pending() int { return m.pending }

// Refreshed returns the last-refresh timestamp. Exposed so the
// standalone wrapper's status bar can show "refreshed=HH:MM:SS".
func (m *Model) Refreshed() time.Time { return m.refreshed }

// Operator returns the operator identity.
func (m *Model) Operator() string { return m.operator }

// Width / Height return the most recent SetSize values.
func (m *Model) Width() int  { return m.width }
func (m *Model) Height() int { return m.height }

// HelpOpen reports whether the help overlay is currently visible.
func (m *Model) HelpOpen() bool { return m.helpOpen }

// ErrMsg returns the current error banner text (empty when no error
// is active).
func (m *Model) ErrMsg() string { return m.errMsg }

func (m *Model) renderHelp() string {
	lines := []string{
		"keymap",
		"  j / ↓     next agent",
		"  k / ↑     previous agent",
		"  g         top",
		"  G         bottom",
		"  Enter     write selected_agent to ui_state KV",
		"  r         refresh (re-call list_agents)",
		"  ?         toggle help",
		"  Esc       dismiss error / close help",
		"  q / ^C    quit",
	}
	return m.th.help.Render(strings.Join(lines, "\n"))
}

// --- Commands ------------------------------------------------------------

func (m *Model) fetchAgentsCmd() tea.Cmd {
	bus := m.bus
	return func() tea.Msg {
		if bus == nil {
			return agentsLoadedMsg{err: fmt.Errorf("no bus configured"), at: time.Now()}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var resp sextantproto.ListAgentsResponse
		err := bus.RPC(ctx, rpc.VerbListAgents, sextantproto.ListAgentsRequest{}, &resp)
		if err != nil {
			return agentsLoadedMsg{err: err, at: time.Now()}
		}
		agents := resp.Agents
		sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
		return agentsLoadedMsg{agents: agents, at: time.Now()}
	}
}

func (m *Model) subscribeLifecycleCmd() tea.Cmd {
	bus := m.bus
	return func() tea.Msg {
		if bus == nil {
			return lifecycleMsg{}
		}
		ch, err := bus.Subscribe(context.Background(), "agents.*.lifecycle")
		if err != nil {
			return agentsLoadedMsg{err: fmt.Errorf("subscribe lifecycle: %w", err), at: time.Now()}
		}
		go drainLifecycle(ch, teaProgramSendOrNoop)
		return lifecycleMsg{}
	}
}

func (m *Model) subscribePendingCmd() tea.Cmd {
	bus := m.bus
	return func() tea.Msg {
		if bus == nil {
			return pendingDeltaMsg{delta: 0}
		}
		ch, err := bus.Subscribe(context.Background(), "user_input.requests.>")
		if err != nil {
			return pendingDeltaMsg{delta: 0}
		}
		go drainPending(ch, teaProgramSendOrNoop)
		return pendingDeltaMsg{delta: 0}
	}
}

func (m *Model) watchSelectedAgentCmd() tea.Cmd {
	op := m.operator
	bus := m.bus
	return func() tea.Msg {
		if bus == nil {
			return selectedAgentMsg{value: ""}
		}
		ch, err := bus.WatchKV(context.Background(), UIStateBucket,
			op+"."+SelectedAgentField)
		if err != nil {
			return selectedAgentMsg{value: ""}
		}
		go drainSelectedAgent(ch, teaProgramSendOrNoop)
		return selectedAgentMsg{value: ""}
	}
}

func (m *Model) putSelectedAgentCmd(value string) tea.Cmd {
	op := m.operator
	bus := m.bus
	return func() tea.Msg {
		if bus == nil {
			return kvPutDoneMsg{err: fmt.Errorf("no bus configured")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := bus.PutKV(ctx, UIStateBucket, op+"."+SelectedAgentField, []byte(value))
		return kvPutDoneMsg{err: err}
	}
}

// drainLifecycle / drainPending / drainSelectedAgent feed channel events
// back into the program. The send function is pluggable so tests can
// observe without a tea.Program.
type sender func(tea.Msg)

// teaProgramSendOrNoop is overwritten in standalone.go once tea.NewProgram
// returns. The default is a no-op so tests that exercise the model
// without a program don't panic.
var teaProgramSendOrNoop sender = func(tea.Msg) {}

// SetSender lets callers (the standalone wrapper, or the dash) install
// the function used to push async messages back into the running
// tea.Program. Returning errors here would race with construction; the
// caller is expected to ensure they only set it once per program run.
func SetSender(send func(tea.Msg)) { teaProgramSendOrNoop = send }

func drainLifecycle(ch <-chan client.Message, send sender) {
	for range ch {
		send(lifecycleMsg{})
	}
}

func drainPending(ch <-chan client.Message, send sender) {
	for msg := range ch {
		if msg.Err != nil {
			continue
		}
		// One subscribe-event per envelope; the count is monotonic in
		// this minimal form. A future pending-queue TUI will correlate
		// request_ids with responses.
		send(pendingDeltaMsg{delta: 1})
	}
}

func drainSelectedAgent(ch <-chan client.KVUpdate, send sender) {
	for u := range ch {
		switch u.Op {
		case client.KVOpDelete, client.KVOpPurge:
			send(selectedAgentMsg{value: ""})
		default:
			send(selectedAgentMsg{value: string(u.Value)})
		}
	}
}

// --- helpers -------------------------------------------------------------

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// MaxGap is the standalone status-bar gap helper, exported so the
// standalone wrapper can match the legacy layout.
func MaxGap(width, left, right int) int {
	if width <= 0 {
		return 2
	}
	gap := width - left - right
	if gap < 2 {
		return 2
	}
	return gap
}

func shortUUID(id uuid.UUID) string {
	s := id.String()
	if len(s) < 8 {
		return s
	}
	return s[:8]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// AbbrSelected returns the short form of the selected_agent UUID for
// the status bar. Exported so the standalone wrapper can reuse it.
func AbbrSelected(s string) string {
	if s == "" {
		return "none"
	}
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}

// stylesAccessor used by standalone.go to render the surrounding
// chrome with the same theme tokens the model uses internally. Local
// types remain unexported.
func (m *Model) styles() localTheme { return m.th }

// Lipgloss compile-time anchor so go vet / unused doesn't drop the
// import in tests that only exercise the reducer.
var _ = lipgloss.Width
