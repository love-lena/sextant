// model.go — agentdetail Component: a DetailPane inspector assembled from
// existing RPCs (get_agent_status + list_agents + worktree_list).
package agentdetail

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/theme"
	"github.com/love-lena/sextant/pkg/tui/component"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// Bus is the RPC-only dependency (a *client.Client satisfies it).
type Bus interface {
	RPC(ctx context.Context, verb string, req, resp any, opts ...client.RPCOption) error
}

// Options configure a Model. AgentID, when set, loads on Init.
type Options struct {
	Bus     Bus
	AgentID string
}

// Model implements component.Component.
type Model struct {
	bus     Bus
	agentID string
	detail  *widget.DetailPane
	loading bool
	errMsg  string
	keys    keymap
	focused bool
	w, h    int
	muted   lipgloss.Style
	errSt   lipgloss.Style
}

type keymap struct {
	Quit    key.Binding
	Refresh key.Binding
	Scroll  key.Binding
}

func defaultKeys() keymap {
	return keymap{
		Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Scroll:  key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "scroll")),
	}
}

// New constructs an agentdetail Model.
func New(opts Options) *Model {
	th := theme.DefaultTheme()
	return &Model{
		bus:     opts.Bus,
		agentID: opts.AgentID,
		detail:  widget.NewDetail(th),
		keys:    defaultKeys(),
		muted:   lipgloss.NewStyle().Foreground(th.ForegroundMuted),
		errSt:   lipgloss.NewStyle().Bold(true).Foreground(th.Danger),
	}
}

// --- messages ---

type detailLoadedMsg struct {
	status   sextantproto.AgentStatus
	template string
	worktree *sextantproto.WorktreeInfo
	err      error
}

// --- Component interface ---

func (m *Model) SetSize(w, h int) {
	m.w, m.h = w, h
	m.detail.SetSize(w, h)
}

func (m *Model) Focus() tea.Cmd { m.focused = true; return nil }
func (m *Model) Blur()          { m.focused = false }
func (m *Model) Focused() bool  { return m.focused }

func (m *Model) ShortHelp() []key.Binding {
	return []key.Binding{m.keys.Scroll, m.keys.Refresh, m.keys.Quit}
}

func (m *Model) FullHelp() [][]key.Binding {
	return [][]key.Binding{{m.keys.Scroll, m.keys.Refresh, m.keys.Quit}}
}

// Init loads the agent if one was supplied at construction.
func (m *Model) Init() tea.Cmd {
	if m.agentID == "" {
		return nil
	}
	m.loading = true
	return m.fetchCmd(m.agentID)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, nil
	case component.LoadMsg:
		m.agentID = msg.ID
		m.loading = true
		return m, m.fetchCmd(msg.ID)
	case detailLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.errMsg = ""
		m.detail.SetSections(buildSections(msg))
		return m, nil
	case tea.KeyMsg:
		if key.Matches(msg, m.keys.Quit) {
			return m, func() tea.Msg { return component.DoneMsg{} }
		}
		if key.Matches(msg, m.keys.Refresh) && m.agentID != "" {
			m.loading = true
			return m, m.fetchCmd(m.agentID)
		}
		cmd := m.detail.Update(msg)
		return m, cmd
	}
	return m, nil
}

func buildSections(d detailLoadedMsg) []widget.Section {
	s := d.status
	dash := func(v string) string {
		if v == "" {
			return "—"
		}
		return v
	}
	agent := widget.Section{Title: "agent", Rows: []widget.Row{
		{Label: "name", Value: dash(s.Name)},
		{Label: "uuid", Value: s.UUID.String()},
		{Label: "lifecycle", Value: dash(s.Lifecycle)},
		{Label: "template", Value: dash(d.template)},
		{Label: "version", Value: fmt.Sprintf("%d", s.Version)},
		{Label: "updated", Value: relTime(s.UpdatedAt)},
	}}
	sections := []widget.Section{agent}

	if s.SessionLog != nil && s.SessionLog.SessionID != "" {
		rows := []widget.Row{
			{Label: "session_id", Value: s.SessionLog.SessionID},
			{Label: "jsonl", Value: s.SessionLog.ContainerJSONLPath},
		}
		if s.SessionLog.SnapshotPath != "" {
			rows = append(rows, widget.Row{Label: "snapshot", Value: s.SessionLog.SnapshotPath})
		}
		sections = append(sections, widget.Section{Title: "session", Rows: rows})
	} else {
		sections = append(sections, widget.Section{Title: "session", Rows: []widget.Row{
			{Label: "state", Value: "no session yet"},
		}})
	}

	if d.worktree != nil {
		w := d.worktree
		sections = append(sections, widget.Section{Title: "worktree", Rows: []widget.Row{
			{Label: "branch", Value: w.Branch + " ⦿ " + w.BaseBranch},
			{Label: "status", Value: string(w.Status)},
			{Label: "path", Value: w.Path},
		}})
	}
	return sections
}

func (m *Model) View() string {
	if m.loading {
		return m.muted.Render("loading agent…")
	}
	if m.errMsg != "" {
		return m.errSt.Render("! " + m.errMsg)
	}
	return m.detail.View()
}

// AgentID returns the currently-loaded agent id.
func (m *Model) AgentID() string { return m.agentID }

// --- commands ---

func (m *Model) fetchCmd(id string) tea.Cmd {
	bus := m.bus
	return func() tea.Msg {
		if bus == nil {
			return detailLoadedMsg{err: fmt.Errorf("no bus configured")}
		}
		uid, err := uuid.Parse(id)
		if err != nil {
			return detailLoadedMsg{err: fmt.Errorf("agent id: %w", err)}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		var statusResp sextantproto.GetAgentStatusResponse
		if err := bus.RPC(ctx, rpc.VerbGetAgentStatus,
			sextantproto.GetAgentStatusRequest{AgentID: uid}, &statusResp); err != nil {
			return detailLoadedMsg{err: err}
		}
		out := detailLoadedMsg{status: statusResp.Status}

		// Template + name (best-effort; degrade to "—" on failure).
		var la sextantproto.ListAgentsResponse
		if bus.RPC(ctx, rpc.VerbListAgents, sextantproto.ListAgentsRequest{}, &la) == nil {
			for _, a := range la.Agents {
				if a.UUID == uid {
					out.template = a.Template
					if out.status.Name == "" {
						out.status.Name = a.Name
					}
					break
				}
			}
		}

		// Owning worktree (best-effort).
		var wl sextantproto.WorktreeListResponse
		if bus.RPC(ctx, rpc.VerbWorktreeList, sextantproto.WorktreeListRequest{}, &wl) == nil {
			for i := range wl.Worktrees {
				if wl.Worktrees[i].OwningAgent == uid {
					out.worktree = &wl.Worktrees[i]
					break
				}
			}
		}
		return out
	}
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("2006-01-02 15:04:05")
}
