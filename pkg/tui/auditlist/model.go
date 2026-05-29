// model.go — auditlist Component: a ListPane over the query_audit RPC.
package auditlist

import (
	"context"
	"fmt"
	"strings"
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

// lookback is the default audit query window.
const lookback = 24 * time.Hour

// queryLimit caps the rows fetched.
const queryLimit = 500

// Bus is the RPC-only dependency (a *client.Client satisfies it).
type Bus interface {
	RPC(ctx context.Context, verb string, req, resp any, opts ...client.RPCOption) error
}

// Options configure a Model.
type Options struct {
	Bus Bus
}

// Model implements component.Component.
type Model struct {
	bus     Bus
	list    *widget.ListPane[sextantproto.QueryAuditRow]
	loading bool
	errMsg  string
	keys    keymap
	focused bool
	w, h    int
	muted   lipgloss.Style
}

type keymap struct {
	Quit    key.Binding
	Open    key.Binding
	Refresh key.Binding
	Nav     key.Binding
}

func defaultKeys() keymap {
	return keymap{
		Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Open:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("⏎", "detail")),
		Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Nav:     key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "nav")),
	}
}

// New constructs an auditlist Model.
func New(opts Options) *Model {
	th := theme.DefaultTheme()
	list := widget.NewList(widget.ListConfig[sextantproto.QueryAuditRow]{
		Header: fmt.Sprintf("%-19s  %-12s  %-22s  %-7s  %s", "TIME", "ACTOR", "ACTION", "RESULT", "AGENT"),
		Render: renderRow,
		Empty:  "  (no audit rows in the last 24h)",
		Filter: func(r sextantproto.QueryAuditRow, q string) bool {
			return strings.Contains(r.Actor, q) || strings.Contains(r.Action, q) ||
				strings.Contains(r.Result, q)
		},
		KeyID: func(r sextantproto.QueryAuditRow) string { return r.ID.String() },
	}, th)
	return &Model{
		bus:   opts.Bus,
		list:  list,
		keys:  defaultKeys(),
		muted: lipgloss.NewStyle().Foreground(th.ForegroundMuted),
	}
}

func renderRow(r sextantproto.QueryAuditRow, selected bool) string {
	prefix := "  "
	if selected {
		prefix = "> "
	}
	return prefix + fmt.Sprintf("%-19s  %-12s  %-22s  %-7s  %s",
		r.Ts.Format("2006-01-02 15:04:05"),
		truncate(r.Actor, 12), truncate(r.Action, 22),
		truncate(r.Result, 7), shortID(r.AgentUUID))
}

// --- messages ---

type rowsLoadedMsg struct {
	rows []sextantproto.QueryAuditRow
	err  error
}

// --- Component interface ---

func (m *Model) SetSize(w, h int) {
	m.w, m.h = w, h
	m.list.SetSize(w, m.listHeight())
}

func (m *Model) listHeight() int {
	if m.errMsg != "" {
		return m.h - 1
	}
	return m.h
}

func (m *Model) Focus() tea.Cmd { m.focused = true; return nil }
func (m *Model) Blur()          { m.focused = false }
func (m *Model) Focused() bool  { return m.focused }

func (m *Model) ShortHelp() []key.Binding {
	return []key.Binding{m.keys.Nav, m.keys.Open, m.keys.Refresh, m.keys.Quit}
}

func (m *Model) FullHelp() [][]key.Binding {
	return [][]key.Binding{{m.keys.Nav, m.keys.Open}, {m.keys.Refresh, m.keys.Quit}}
}

// Init loads the audit rows.
func (m *Model) Init() tea.Cmd {
	m.loading = true
	return m.fetchCmd()
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, nil
	case component.LoadMsg:
		m.loading = true
		return m, m.fetchCmd()
	case rowsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = fmt.Sprintf("query_audit: %v", msg.err)
			m.list.SetSize(m.w, m.listHeight())
			return m, nil
		}
		m.errMsg = ""
		m.list.SetRows(msg.rows)
		m.list.SetSize(m.w, m.listHeight())
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.list.Filtering() && key.Matches(msg, m.keys.Quit) {
		return m, func() tea.Msg { return component.DoneMsg{} }
	}
	if !m.list.Filtering() && key.Matches(msg, m.keys.Refresh) {
		m.loading = true
		return m, m.fetchCmd()
	}
	act := m.list.Update(msg)
	if act.Kind == widget.ListSelected && act.HasRow {
		id := act.Row.ID.String()
		return m, func() tea.Msg { return component.OpenMsg{Target: "audit-detail", ID: id} }
	}
	return m, nil
}

func (m *Model) View() string {
	if m.loading && m.list.Len() == 0 {
		return m.muted.Render("loading audit log…")
	}
	if m.errMsg != "" {
		return "! " + m.errMsg + "\n" + m.list.View()
	}
	return m.list.View()
}

// Count returns the visible row count (for tests).
func (m *Model) Count() int { return m.list.Len() }

// Selected returns the focused audit row (for tests / the dash router).
func (m *Model) Selected() (sextantproto.QueryAuditRow, bool) { return m.list.Selected() }

// --- commands ---

func (m *Model) fetchCmd() tea.Cmd {
	bus := m.bus
	return func() tea.Msg {
		if bus == nil {
			return rowsLoadedMsg{err: fmt.Errorf("no bus configured")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		req := sextantproto.QueryAuditRequest{
			TimeRange: sextantproto.TimeRange{Since: time.Now().Add(-lookback)},
			Limit:     queryLimit,
		}
		var resp sextantproto.QueryAuditResponse
		if err := bus.RPC(ctx, rpc.VerbQueryAudit, req, &resp); err != nil {
			return rowsLoadedMsg{err: err}
		}
		return rowsLoadedMsg{rows: resp.Rows}
	}
}

// --- helpers ---

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	if n == 1 {
		return s[:1]
	}
	return s[:n-1] + "…"
}

func shortID(id uuid.UUID) string {
	s := id.String()
	if len(s) < 8 {
		return s
	}
	return s[:8]
}
