// model.go — Bubble Tea model for sextant-tui-agents.
//
// Separated from main.go so main.go stays close to the conventions's
// "~150 LOC TUI" budget and so the model can be exercised directly
// from model_test.go without booting NATS.
//
// The model is the only place that touches Bubble Tea types; main.go
// wires flags, identity, and the live client.Client.
//
// Plan: plans/bootstrap.md#M13
package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// uiStateBucket and selectedAgentField mirror conventions/tui-conventions.md
// §"ui.state.* keys". One bucket holds every operator's keys; the wire-
// key shape is `<operator>.<field>` (the `ui.state` prefix is implicit
// in the bucket).
const (
	uiStateBucket      = "ui_state"
	selectedAgentField = "selected_agent"
	noneSelection      = "none"
)

// agentBus is the Client-shape subset the model depends on. Defined as
// an interface so tests can drive the model without touching NATS.
//
// The shape mirrors pkg/client method signatures so a *client.Client
// satisfies it directly.
type agentBus interface {
	RPC(ctx context.Context, verb string, req, resp any, opts ...client.RPCOption) error
	Subscribe(ctx context.Context, subject string, opts ...client.SubscribeOption) (<-chan client.Message, error)
	WatchKV(ctx context.Context, bucket, key string) (<-chan client.KVUpdate, error)
	PutKV(ctx context.Context, bucket, key string, value []byte) error
}

// model is the Bubble Tea model. Exported fields are stable for tests;
// everything else is package-private.
type model struct {
	bus      agentBus
	operator string
	th       theme

	agents    []sextantproto.AgentSummary
	cursor    int
	selected  string // selected agent UUID per ui_state KV; "" or "none" = no selection
	pending   int
	refreshed time.Time
	helpOpen  bool
	errMsg    string
	width     int
	height    int
}

// newModel builds an empty model. ctx is unused at construction but
// kept on the signature so callers can wire cancellation symmetrically
// with future TUIs.
func newModel(bus agentBus, operator string) *model {
	return &model{bus: bus, operator: operator, th: defaultTheme()}
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
// M13 just counts subscribe-events; a TUI focused on the pending queue
// will do the real correlation work.
type pendingDeltaMsg struct{ delta int }

// selectedAgentMsg lands when the `ui_state` KV key changes (possibly
// written by us, possibly by a sibling TUI).
type selectedAgentMsg struct{ value string }

// kvPutDoneMsg lands when our own PutKV completes; surfaces errors.
type kvPutDoneMsg struct{ err error }

// --- Init ----------------------------------------------------------------

// Init returns the commands the program runs at startup: list_agents,
// the three subscriptions, and the KV watch.
func (m *model) Init() tea.Cmd {
	return tea.Batch(
		m.fetchAgentsCmd(),
		m.subscribeLifecycleCmd(),
		m.subscribePendingCmd(),
		m.watchSelectedAgentCmd(),
	)
}

// --- Update --------------------------------------------------------------

// Update is the Bubble Tea reducer. Returns the next model + any
// follow-up command.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
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

// handleKey dispatches the keymap from conventions/tui-conventions.md.
func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.helpOpen {
		switch msg.String() {
		case "?", "esc", "q", "ctrl+c":
			m.helpOpen = false
		}
		return m, nil
	}
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "?":
		m.helpOpen = true
	case "esc":
		m.errMsg = ""
	case "j", "down":
		if m.cursor < len(m.agents)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "g":
		m.cursor = 0
	case "G":
		m.cursor = max0(len(m.agents) - 1)
	case "r":
		return m, m.fetchAgentsCmd()
	case "enter":
		if len(m.agents) > 0 {
			return m, m.putSelectedAgentCmd(m.agents[m.cursor].UUID.String())
		}
	}
	return m, nil
}

// --- View ----------------------------------------------------------------

// View renders the full TUI.
func (m *model) View() string {
	var b strings.Builder
	b.WriteString(m.th.title.Render("sextant agents"))
	b.WriteString("  ")
	b.WriteString(m.th.muted.Render(fmt.Sprintf("operator=%s", m.operator)))
	b.WriteString("\n\n")
	if m.errMsg != "" {
		b.WriteString(m.th.errorBar.Render("! " + m.errMsg))
		b.WriteString("  ")
		b.WriteString(m.th.muted.Render("(Esc to dismiss)"))
		b.WriteString("\n")
	}
	b.WriteString(m.renderTable())
	b.WriteString("\n")
	b.WriteString(m.renderStatusBar())
	if m.helpOpen {
		b.WriteString("\n\n")
		b.WriteString(m.renderHelp())
	}
	return b.String()
}

func (m *model) renderTable() string {
	header := fmt.Sprintf("%-24s  %-8s  %-16s  %s",
		"NAME", "UUID", "TEMPLATE", "LIFECYCLE")
	var rows []string
	rows = append(rows, m.th.header.Render(header))
	if len(m.agents) == 0 {
		rows = append(rows, m.th.muted.Render("  (no agents yet — spawn one with `sextant agents spawn`)"))
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
		case m.selected != "" && m.selected != noneSelection && a.UUID.String() == m.selected:
			// Sibling-TUI selection — mark with a leading bullet without
			// reversing the row.
			rows = append(rows, m.th.row.Render("> "+row))
		default:
			rows = append(rows, m.th.row.Render("  "+row))
		}
	}
	return strings.Join(rows, "\n")
}

func (m *model) renderStatusBar() string {
	left := fmt.Sprintf("agents=%d  selected=%s", len(m.agents), abbrSelected(m.selected))
	if !m.refreshed.IsZero() {
		left += "  refreshed=" + m.refreshed.Format("15:04:05")
	}
	right := fmt.Sprintf("pending=%d  [j/k] nav  [Enter] select  [r] reload  [?] help  [q] quit", m.pending)
	gap := strings.Repeat(" ", maxGap(m.width, lipgloss.Width(left), lipgloss.Width(right)))
	return m.th.status.Render(left + gap + right)
}

func (m *model) renderHelp() string {
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

func (m *model) fetchAgentsCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var resp sextantproto.ListAgentsResponse
		err := m.bus.RPC(ctx, rpc.VerbListAgents, sextantproto.ListAgentsRequest{}, &resp)
		if err != nil {
			return agentsLoadedMsg{err: err, at: time.Now()}
		}
		agents := resp.Agents
		sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
		return agentsLoadedMsg{agents: agents, at: time.Now()}
	}
}

func (m *model) subscribeLifecycleCmd() tea.Cmd {
	return func() tea.Msg {
		// Use Background ctx — the channel closes via Client.Close on
		// program shutdown. tea.Program runs the listener loop below
		// until the channel is closed.
		ch, err := m.bus.Subscribe(context.Background(), "agents.*.lifecycle")
		if err != nil {
			return agentsLoadedMsg{err: fmt.Errorf("subscribe lifecycle: %w", err), at: time.Now()}
		}
		go drainLifecycle(ch, teaProgramSendOrNoop)
		return lifecycleMsg{} // first refresh-on-subscribe so the list catches whatever happened pre-attach
	}
}

func (m *model) subscribePendingCmd() tea.Cmd {
	return func() tea.Msg {
		ch, err := m.bus.Subscribe(context.Background(), "user_input.requests.>")
		if err != nil {
			// Pending count is best-effort; a subscription failure does
			// not break the TUI. Swallow.
			return pendingDeltaMsg{delta: 0}
		}
		go drainPending(ch, teaProgramSendOrNoop)
		return pendingDeltaMsg{delta: 0}
	}
}

func (m *model) watchSelectedAgentCmd() tea.Cmd {
	op := m.operator
	return func() tea.Msg {
		ch, err := m.bus.WatchKV(context.Background(), uiStateBucket,
			op+"."+selectedAgentField)
		if err != nil {
			return selectedAgentMsg{value: ""}
		}
		go drainSelectedAgent(ch, teaProgramSendOrNoop)
		return selectedAgentMsg{value: ""}
	}
}

func (m *model) putSelectedAgentCmd(value string) tea.Cmd {
	op := m.operator
	bus := m.bus
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := bus.PutKV(ctx, uiStateBucket, op+"."+selectedAgentField, []byte(value))
		return kvPutDoneMsg{err: err}
	}
}

// drainLifecycle / drainPending / drainSelectedAgent feed channel events
// back into the program. The send function is pluggable so tests can
// observe without a tea.Program.
type sender func(tea.Msg)

// teaProgramSendOrNoop is overwritten in main.go once tea.NewProgram
// returns. The default is a no-op so tests that exercise the model
// without a program don't panic.
var teaProgramSendOrNoop sender = func(tea.Msg) {}

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
		// One subscribe-event per envelope. Responses arrive on a
		// different subject so we never see them here — the count is
		// monotonic in this M13 minimal form. A future pending-queue
		// TUI will correlate request_ids with responses; here we just
		// surface "non-zero means somebody is asking for input".
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

func maxGap(width, left, right int) int {
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

func abbrSelected(s string) string {
	if s == "" {
		return "none"
	}
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}
