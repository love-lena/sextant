// model.go — pending Component, composed from widget.ListPane and a
// widget.SubscribeSource over `user_input.>`.
package pending

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/theme"
	"github.com/love-lena/sextant/pkg/tui/component"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// Bus is the subscribe-only dependency (a *client.Client satisfies it).
type Bus interface {
	widget.SubscribeBus
}

// Options configure a Model.
type Options struct {
	Bus Bus
}

// Request is one unanswered user_input request.
type Request struct {
	RequestID uuid.UUID
	FromUUID  uuid.UUID
	Question  string
	Urgency   string
	Ts        time.Time
}

// Model implements component.Component.
type Model struct {
	bus      Bus
	list     *widget.ListPane[Request]
	requests map[uuid.UUID]Request
	answered map[uuid.UUID]bool
	keys     keymap
	focused  bool
	errMsg   string
	w, h     int
}

type keymap struct {
	Quit   key.Binding
	Answer key.Binding
	Nav    key.Binding
	Filter key.Binding
}

func defaultKeys() keymap {
	return keymap{
		Quit:   key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Answer: key.NewBinding(key.WithKeys("enter"), key.WithHelp("⏎", "answer")),
		Nav:    key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "nav")),
		Filter: key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	}
}

// New constructs an empty pending Model.
func New(opts Options) *Model {
	th := theme.DefaultTheme()
	list := widget.NewList(widget.ListConfig[Request]{
		Header: fmt.Sprintf("%-10s  %-10s  %s", "URGENCY", "FROM", "QUESTION"),
		Render: renderRow,
		Empty:  "  (no pending requests)",
		Filter: func(r Request, q string) bool {
			return strings.Contains(r.Question, q) ||
				strings.Contains(r.Urgency, q) ||
				strings.Contains(r.FromUUID.String(), q)
		},
		KeyID: func(r Request) string { return r.RequestID.String() },
	}, th)
	return &Model{
		bus:      opts.Bus,
		list:     list,
		requests: map[uuid.UUID]Request{},
		answered: map[uuid.UUID]bool{},
		keys:     defaultKeys(),
	}
}

func renderRow(r Request, selected bool) string {
	prefix := "  "
	if selected {
		prefix = "> "
	}
	return prefix + fmt.Sprintf("%-10s  %-10s  %s",
		truncate(r.Urgency, 10), shortID(r.FromUUID), truncate(r.Question, 56))
}

// --- messages ---

type requestUpsertMsg struct{ req Request }
type responseMsg struct{ requestID uuid.UUID }
type subErrMsg struct{ err error }

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
	return []key.Binding{m.keys.Nav, m.keys.Answer, m.keys.Filter, m.keys.Quit}
}
func (m *Model) FullHelp() [][]key.Binding {
	return [][]key.Binding{{m.keys.Nav, m.keys.Answer}, {m.keys.Filter, m.keys.Quit}}
}

// Init subscribes to the full user_input stream (deliver-all so the
// snapshot of outstanding requests lands).
func (m *Model) Init() tea.Cmd { return m.subscribeCmd() }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, nil
	case component.LoadMsg:
		return m, nil // pending isn't scoped to an ID
	case requestUpsertMsg:
		m.requests[msg.req.RequestID] = msg.req
		m.recompute()
		return m, nil
	case responseMsg:
		m.answered[msg.requestID] = true
		m.recompute()
		return m, nil
	case subErrMsg:
		m.errMsg = fmt.Sprintf("subscribe: %v", msg.err)
		m.list.SetSize(m.w, m.listHeight())
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func emitDone() tea.Msg { return component.DoneMsg{} }

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Quit takes priority, but not while the list's filter input is active
	// (so the operator can type "q" into a filter).
	if !m.list.Filtering() && key.Matches(msg, m.keys.Quit) {
		return m, emitDone
	}
	if msg.String() == "esc" && m.errMsg != "" && !m.list.Filtering() {
		m.errMsg = ""
		m.list.SetSize(m.w, m.listHeight())
		return m, nil
	}
	act := m.list.Update(msg)
	if act.Kind == widget.ListSelected && act.HasRow {
		id := act.Row.RequestID.String()
		return m, func() tea.Msg {
			return component.OpenMsg{Target: "pending-answer", ID: id}
		}
	}
	return m, nil
}

func (m *Model) recompute() {
	out := make([]Request, 0, len(m.requests))
	for id, r := range m.requests {
		if m.answered[id] {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Ts.Equal(out[j].Ts) {
			return out[i].Ts.Before(out[j].Ts)
		}
		return out[i].RequestID.String() < out[j].RequestID.String()
	})
	m.list.SetRows(out)
}

func (m *Model) View() string {
	if m.errMsg != "" {
		return "! " + m.errMsg + "\n" + m.list.View()
	}
	return m.list.View()
}

// Count returns the number of unanswered requests currently shown.
func (m *Model) Count() int { return m.list.Len() }

// Selected returns the highlighted request (for tests / the dash router).
func (m *Model) Selected() (Request, bool) { return m.list.Selected() }

// --- commands + drain ---

func (m *Model) subscribeCmd() tea.Cmd {
	bus := m.bus
	return func() tea.Msg {
		if bus == nil {
			return subErrMsg{err: fmt.Errorf("no bus configured")}
		}
		src := widget.SubscribeSource(context.Background(), bus, "user_input.>", client.WithDeliverAll())
		go widget.Pump(context.Background(), src, teaProgramSendOrNoop, decodeItem, func(err error) tea.Msg {
			return subErrMsg{err: err}
		})
		return nil
	}
}

// decodeItem maps a bus message to a model message (nil = ignore).
func decodeItem(msg client.Message) tea.Msg {
	if msg.Err != nil {
		return nil
	}
	switch msg.Envelope.Kind {
	case sextantproto.KindUserInputRequest:
		var p sextantproto.UserInputRequestPayload
		if json.Unmarshal(msg.Envelope.Payload, &p) != nil {
			return nil
		}
		return requestUpsertMsg{req: Request{
			RequestID: p.RequestID, FromUUID: p.FromUUID,
			Question: p.Question, Urgency: p.Urgency, Ts: msg.Timestamp,
		}}
	case sextantproto.KindUserInputResponse:
		var p sextantproto.UserInputResponsePayload
		if json.Unmarshal(msg.Envelope.Payload, &p) != nil {
			return nil
		}
		return responseMsg{requestID: p.RequestID}
	}
	return nil
}

type sender func(tea.Msg)

var teaProgramSendOrNoop sender = func(tea.Msg) {}

// SetSender installs the function that pushes async messages into the
// running tea.Program (mirrors pkg/tui/agents.SetSender).
func SetSender(send func(tea.Msg)) { teaProgramSendOrNoop = send }

// --- helpers ---

func shortID(id uuid.UUID) string {
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
