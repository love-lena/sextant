// model.go — traces Component: an interactive span-tree outline over a
// query_trace result, built on widget.ListPane fed FlattenVisible rows.
package traces

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

// Options configure a Model. TraceID, when set, loads on Init.
type Options struct {
	Bus     Bus
	TraceID string
}

// Model implements component.Component.
type Model struct {
	bus       Bus
	traceID   string
	list      *widget.ListPane[Row]
	roots     []*Node
	collapsed map[string]bool
	loading   bool
	errMsg    string
	keys      keymap
	focused   bool
	w, h      int

	muted lipgloss.Style
	errSt lipgloss.Style
}

type keymap struct {
	Quit     key.Binding
	Toggle   key.Binding
	Collapse key.Binding
	Nav      key.Binding
}

func defaultKeys() keymap {
	return keymap{
		Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Toggle:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("⏎", "expand/collapse")),
		Collapse: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "collapse")),
		Nav:      key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "nav")),
	}
}

// New constructs a traces Model.
func New(opts Options) *Model {
	th := theme.DefaultTheme()
	m := &Model{
		bus:       opts.Bus,
		traceID:   opts.TraceID,
		collapsed: map[string]bool{},
		keys:      defaultKeys(),
		muted:     lipgloss.NewStyle().Foreground(th.ForegroundMuted),
		errSt:     lipgloss.NewStyle().Bold(true).Foreground(th.Danger),
	}
	m.list = widget.NewList(widget.ListConfig[Row]{
		Render: m.renderRow,
		Empty:  "  (no spans)",
		KeyID:  func(r Row) string { return r.Span.SpanID },
	}, th)
	return m
}

func (m *Model) renderRow(r Row, selected bool) string {
	marker := "  "
	if r.HasChildren {
		if m.collapsed[r.Span.SpanID] {
			marker = "▸ "
		} else {
			marker = "▾ "
		}
	}
	dur := time.Duration(r.Span.DurationNanos)
	line := fmt.Sprintf("%s%s%s (%s)", strings.Repeat("  ", r.Depth), marker, r.Span.SpanName, dur)
	if sc := r.Span.StatusCode; sc != "" && sc != "OK" && sc != "STATUS_CODE_OK" {
		line += " [" + sc + "]"
	}
	if selected {
		return "> " + line
	}
	return "  " + line
}

// --- messages ---

type spansLoadedMsg struct {
	spans []sextantproto.TraceSpan
	err   error
}

// --- Component interface ---

func (m *Model) SetSize(w, h int) {
	m.w, m.h = w, h
	m.list.SetSize(w, h)
}

func (m *Model) Focus() tea.Cmd { m.focused = true; return nil }
func (m *Model) Blur()          { m.focused = false }
func (m *Model) Focused() bool  { return m.focused }

func (m *Model) ShortHelp() []key.Binding {
	return []key.Binding{m.keys.Nav, m.keys.Toggle, m.keys.Collapse, m.keys.Quit}
}

func (m *Model) FullHelp() [][]key.Binding {
	return [][]key.Binding{{m.keys.Nav, m.keys.Toggle}, {m.keys.Collapse, m.keys.Quit}}
}

// Init loads the trace if one was supplied at construction.
func (m *Model) Init() tea.Cmd {
	if m.traceID == "" {
		return nil
	}
	m.loading = true
	return m.fetchCmd(m.traceID)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, nil
	case component.LoadMsg:
		m.traceID = msg.ID
		m.loading = true
		return m, m.fetchCmd(msg.ID)
	case spansLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = fmt.Sprintf("query_trace: %v", msg.err)
			return m, nil
		}
		m.errMsg = ""
		m.roots = BuildSpanTree(msg.spans)
		m.collapsed = map[string]bool{}
		m.rebuild()
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func emitDone() tea.Msg { return component.DoneMsg{} }

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, emitDone
	case key.Matches(msg, m.keys.Collapse):
		if r, ok := m.list.Selected(); ok && r.HasChildren {
			m.collapsed[r.Span.SpanID] = true
			m.rebuild()
		}
		return m, nil
	}
	act := m.list.Update(msg)
	if act.Kind == widget.ListSelected && act.HasRow && act.Row.HasChildren {
		id := act.Row.Span.SpanID
		m.collapsed[id] = !m.collapsed[id]
		m.rebuild()
	}
	return m, nil
}

func (m *Model) rebuild() {
	m.list.SetRows(FlattenVisible(m.roots, m.collapsed))
}

func (m *Model) View() string {
	if m.loading {
		return m.muted.Render("loading trace…")
	}
	if m.errMsg != "" {
		return m.errSt.Render("! "+m.errMsg) + "\n" + m.list.View()
	}
	return m.list.View()
}

// VisibleRows returns the current flattened row count (for tests).
func (m *Model) VisibleRows() int { return m.list.Len() }

// TraceID returns the currently-loaded trace id.
func (m *Model) TraceID() string { return m.traceID }

// --- commands ---

func (m *Model) fetchCmd(traceID string) tea.Cmd {
	bus := m.bus
	return func() tea.Msg {
		if bus == nil {
			return spansLoadedMsg{err: fmt.Errorf("no bus configured")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var resp sextantproto.QueryTraceResponse
		if err := bus.RPC(ctx, rpc.VerbQueryTrace,
			sextantproto.QueryTraceRequest{TraceID: traceID}, &resp); err != nil {
			return spansLoadedMsg{err: err}
		}
		return spansLoadedMsg{spans: resp.Spans}
	}
}
