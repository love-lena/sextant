// model.go — worktreelist Component: a ListPane over the worktree_list
// RPC.
package worktreelist

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

// Options configure a Model.
type Options struct {
	Bus Bus
}

// Model implements component.Component.
type Model struct {
	bus     Bus
	list    *widget.ListPane[sextantproto.WorktreeInfo]
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
		Open:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("⏎", "diff")),
		Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Nav:     key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "nav")),
	}
}

// New constructs a worktreelist Model.
func New(opts Options) *Model {
	th := theme.DefaultTheme()
	list := widget.NewList(widget.ListConfig[sextantproto.WorktreeInfo]{
		Header: fmt.Sprintf("%-22s  %-22s  %-9s  %s", "NAME", "BRANCH", "STATUS", "LAST ACTIVITY"),
		Render: renderRow,
		Empty:  "  (no worktrees)",
		Filter: func(w sextantproto.WorktreeInfo, q string) bool {
			return strings.Contains(w.Name, q) || strings.Contains(w.Branch, q) || strings.Contains(string(w.Status), q)
		},
		KeyID: func(w sextantproto.WorktreeInfo) string { return w.Name },
	}, th)
	return &Model{
		bus:   opts.Bus,
		list:  list,
		keys:  defaultKeys(),
		muted: lipgloss.NewStyle().Foreground(th.ForegroundMuted),
	}
}

func renderRow(w sextantproto.WorktreeInfo, selected bool) string {
	prefix := "  "
	if selected {
		prefix = "> "
	}
	branch := truncate(w.Branch, 18) + " ⦿ " + truncate(w.BaseBranch, 0)
	return prefix + fmt.Sprintf("%-22s  %-22s  %-9s  %s",
		truncate(w.Name, 22), truncate(branch, 22), truncate(string(w.Status), 9), age(w.LastActivity))
}

// --- messages ---

type worktreesLoadedMsg struct {
	worktrees []sextantproto.WorktreeInfo
	err       error
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

// Init loads the worktree list.
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
	case worktreesLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = fmt.Sprintf("worktree_list: %v", msg.err)
			m.list.SetSize(m.w, m.listHeight())
			return m, nil
		}
		m.errMsg = ""
		m.list.SetRows(msg.worktrees)
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
		name := act.Row.Name
		return m, func() tea.Msg { return component.OpenMsg{Target: "worktree-diff", ID: name} }
	}
	return m, nil
}

func (m *Model) View() string {
	if m.loading && m.list.Len() == 0 {
		return m.muted.Render("loading worktrees…")
	}
	if m.errMsg != "" {
		return "! " + m.errMsg + "\n" + m.list.View()
	}
	return m.list.View()
}

// Count returns the visible worktree count (for tests).
func (m *Model) Count() int { return m.list.Len() }

// Selected returns the focused worktree (for tests / the dash router).
func (m *Model) Selected() (sextantproto.WorktreeInfo, bool) { return m.list.Selected() }

// --- commands ---

func (m *Model) fetchCmd() tea.Cmd {
	bus := m.bus
	return func() tea.Msg {
		if bus == nil {
			return worktreesLoadedMsg{err: fmt.Errorf("no bus configured")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var resp sextantproto.WorktreeListResponse
		if err := bus.RPC(ctx, rpc.VerbWorktreeList, sextantproto.WorktreeListRequest{}, &resp); err != nil {
			return worktreesLoadedMsg{err: err}
		}
		return worktreesLoadedMsg{worktrees: resp.Worktrees}
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

func age(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("2006-01-02 15:04")
}
