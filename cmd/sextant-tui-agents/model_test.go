// model_test.go — unit tests for the sextant-tui-agents Bubble Tea
// model. Drives the reducer directly with crafted messages; does NOT
// boot NATS. The model uses an agentBus interface so we can substitute
// a fake here.
//
// Plan: plans/bootstrap.md#M13
package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// fakeBus implements agentBus. Each method has knobs so individual
// tests can pin behavior without juggling channels they don't care about.
type fakeBus struct {
	mu sync.Mutex

	rpcResp     sextantproto.ListAgentsResponse
	rpcErr      error
	rpcCalls    int
	subscribeCh chan client.Message
	watchCh     chan client.KVUpdate
	putCalls    []putCall
	putErr      error
}

type putCall struct {
	bucket string
	key    string
	value  string
}

func (f *fakeBus) RPC(_ context.Context, _ string, _, resp any, _ ...client.RPCOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rpcCalls++
	if f.rpcErr != nil {
		return f.rpcErr
	}
	out, ok := resp.(*sextantproto.ListAgentsResponse)
	if !ok {
		return errors.New("fakeBus: unexpected resp type")
	}
	*out = f.rpcResp
	return nil
}

func (f *fakeBus) Subscribe(_ context.Context, _ string, _ ...client.SubscribeOption) (<-chan client.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.subscribeCh == nil {
		f.subscribeCh = make(chan client.Message, 4)
	}
	return f.subscribeCh, nil
}

func (f *fakeBus) WatchKV(_ context.Context, _, _ string) (<-chan client.KVUpdate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.watchCh == nil {
		f.watchCh = make(chan client.KVUpdate, 4)
	}
	return f.watchCh, nil
}

func (f *fakeBus) PutKV(_ context.Context, bucket, key string, value []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putCalls = append(f.putCalls, putCall{bucket: bucket, key: key, value: string(value)})
	return f.putErr
}

func (f *fakeBus) putCallsCopy() []putCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]putCall, len(f.putCalls))
	copy(out, f.putCalls)
	return out
}

// summary builds a deterministic AgentSummary for tests.
func summary(name, lifecycle, template string) sextantproto.AgentSummary {
	return sextantproto.AgentSummary{
		UUID:      uuid.MustParse("11111111-2222-3333-4444-555555555555"),
		Name:      name,
		Template:  template,
		Lifecycle: lifecycle,
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func summaryWithUUID(name string, id uuid.UUID) sextantproto.AgentSummary {
	return sextantproto.AgentSummary{
		UUID:      id,
		Name:      name,
		Lifecycle: "defined",
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestAgentsLoadedPopulatesList(t *testing.T) {
	bus := &fakeBus{rpcResp: sextantproto.ListAgentsResponse{
		Agents: []sextantproto.AgentSummary{summary("alpha", "running", "claude-coder")},
	}}
	m := newModel(bus, "lena")
	now := time.Now()
	got, _ := m.Update(agentsLoadedMsg{agents: bus.rpcResp.Agents, at: now})
	mm := got.(*model)
	if len(mm.agents) != 1 || mm.agents[0].Name != "alpha" {
		t.Fatalf("agents not populated: %+v", mm.agents)
	}
	if !mm.refreshed.Equal(now) {
		t.Fatalf("refreshed = %v, want %v", mm.refreshed, now)
	}
	if mm.errMsg != "" {
		t.Fatalf("errMsg = %q, want empty", mm.errMsg)
	}
}

func TestAgentsLoadedSurfacesRPCError(t *testing.T) {
	m := newModel(&fakeBus{}, "lena")
	got, _ := m.Update(agentsLoadedMsg{err: errors.New("boom"), at: time.Now()})
	mm := got.(*model)
	if mm.errMsg == "" {
		t.Fatal("errMsg should be populated on RPC error")
	}
}

func TestArrowKeysMoveCursorWithinBounds(t *testing.T) {
	m := newModel(&fakeBus{}, "lena")
	m.agents = []sextantproto.AgentSummary{
		summaryWithUUID("a", uuid.New()),
		summaryWithUUID("b", uuid.New()),
		summaryWithUUID("c", uuid.New()),
	}
	cases := []struct {
		key  string
		want int
	}{
		{"j", 1},
		{"down", 2},
		{"j", 2}, // clamped
		{"k", 1},
		{"up", 0},
		{"k", 0}, // clamped
		{"G", 2},
		{"g", 0},
	}
	for _, tc := range cases {
		out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.key)})
		if tc.key == "down" || tc.key == "up" {
			out, _ = m.Update(specialKey(tc.key))
		}
		mm := out.(*model)
		if mm.cursor != tc.want {
			t.Fatalf("after %q: cursor = %d, want %d", tc.key, mm.cursor, tc.want)
		}
		m = mm
	}
}

func TestEnterTriggersPutKVWithSelectedAgentUUID(t *testing.T) {
	bus := &fakeBus{}
	m := newModel(bus, "lena")
	id := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	m.agents = []sextantproto.AgentSummary{summaryWithUUID("alpha", id)}

	_, cmd := m.Update(specialKey("enter"))
	if cmd == nil {
		t.Fatal("Enter should issue a command")
	}
	// Execute the command — it returns a kvPutDoneMsg after invoking PutKV.
	msg := cmd()
	if done, ok := msg.(kvPutDoneMsg); !ok {
		t.Fatalf("cmd returned %T, want kvPutDoneMsg", msg)
	} else if done.err != nil {
		t.Fatalf("kvPutDoneMsg.err = %v", done.err)
	}
	calls := bus.putCallsCopy()
	if len(calls) != 1 {
		t.Fatalf("PutKV calls = %d, want 1", len(calls))
	}
	got := calls[0]
	if got.bucket != uiStateBucket {
		t.Errorf("bucket = %q, want %q", got.bucket, uiStateBucket)
	}
	wantKey := "lena.selected_agent"
	if got.key != wantKey {
		t.Errorf("key = %q, want %q", got.key, wantKey)
	}
	if got.value != id.String() {
		t.Errorf("value = %q, want %q", got.value, id.String())
	}
}

func TestEnterWithoutAgentsIsNoop(t *testing.T) {
	bus := &fakeBus{}
	m := newModel(bus, "lena")
	_, cmd := m.Update(specialKey("enter"))
	if cmd != nil {
		// Some commands are pure no-ops; ensure we don't actually call PutKV.
		_ = cmd()
	}
	if len(bus.putCallsCopy()) != 0 {
		t.Fatal("Enter on empty list should not call PutKV")
	}
}

func TestRRefreshIssuesListAgentsRPC(t *testing.T) {
	bus := &fakeBus{rpcResp: sextantproto.ListAgentsResponse{
		Agents: []sextantproto.AgentSummary{summary("alpha", "running", "")},
	}}
	m := newModel(bus, "lena")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("'r' must issue an RPC command")
	}
	msg := cmd()
	loaded, ok := msg.(agentsLoadedMsg)
	if !ok {
		t.Fatalf("got %T, want agentsLoadedMsg", msg)
	}
	if loaded.err != nil {
		t.Fatalf("rpc err = %v", loaded.err)
	}
	if len(loaded.agents) != 1 {
		t.Fatalf("agents loaded = %d", len(loaded.agents))
	}
}

func TestHelpToggleAndEscClose(t *testing.T) {
	m := newModel(&fakeBus{}, "lena")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	mm := out.(*model)
	if !mm.helpOpen {
		t.Fatal("? should open help")
	}
	out, _ = mm.Update(specialKey("esc"))
	mm = out.(*model)
	if mm.helpOpen {
		t.Fatal("esc should close help")
	}
}

func TestSelectedAgentMsgUpdatesModel(t *testing.T) {
	m := newModel(&fakeBus{}, "lena")
	out, _ := m.Update(selectedAgentMsg{value: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"})
	mm := out.(*model)
	if mm.selected != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("selected = %q", mm.selected)
	}
}

func TestPendingDeltaClampsAtZero(t *testing.T) {
	m := newModel(&fakeBus{}, "lena")
	out, _ := m.Update(pendingDeltaMsg{delta: 2})
	mm := out.(*model)
	if mm.pending != 2 {
		t.Fatalf("pending = %d, want 2", mm.pending)
	}
	out, _ = mm.Update(pendingDeltaMsg{delta: -5})
	mm = out.(*model)
	if mm.pending != 0 {
		t.Fatalf("pending = %d, want 0 (clamped)", mm.pending)
	}
}

func TestQuitKeyEmitsQuitCmd(t *testing.T) {
	m := newModel(&fakeBus{}, "lena")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q must emit tea.Quit")
	}
	// Quit is a func that returns tea.QuitMsg{}.
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("q cmd did not return tea.QuitMsg")
	}
}

func TestViewRendersWithoutPanic(t *testing.T) {
	m := newModel(&fakeBus{}, "lena")
	m.width, m.height = 100, 30
	if got := m.View(); got == "" {
		t.Fatal("empty view")
	}
	m.agents = []sextantproto.AgentSummary{summary("alpha", "running", "claude-coder")}
	if got := m.View(); got == "" {
		t.Fatal("empty view with agents")
	}
	m.errMsg = "boom"
	if got := m.View(); got == "" {
		t.Fatal("empty view with err")
	}
	m.helpOpen = true
	if got := m.View(); got == "" {
		t.Fatal("empty view with help")
	}
}

func TestSanitizeOperator(t *testing.T) {
	cases := map[string]string{
		"lena":          "lena",
		"lena.dev":      "lena_dev",
		"User Name":     "User_Name",
		"alice@example": "alice_example",
		"":              "",
	}
	for in, want := range cases {
		if got := sanitizeOperator(in); got != want {
			t.Errorf("sanitizeOperator(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveOperatorPrefersFlag(t *testing.T) {
	got, err := resolveOperator("explicit")
	if err != nil {
		t.Fatalf("resolveOperator: %v", err)
	}
	if got != "explicit" {
		t.Fatalf("got %q, want %q", got, "explicit")
	}
}

func TestResolveOperatorFallsBackToEnv(t *testing.T) {
	t.Setenv("SEXTANT_OPERATOR", "from-env")
	got, err := resolveOperator("")
	if err != nil {
		t.Fatalf("resolveOperator: %v", err)
	}
	if got != "from-env" {
		t.Fatalf("got %q, want %q", got, "from-env")
	}
}

// specialKey turns "down" / "up" / "esc" / "enter" into the proper
// tea.KeyMsg the reducer matches on.
func specialKey(name string) tea.KeyMsg {
	switch name {
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
	}
}
