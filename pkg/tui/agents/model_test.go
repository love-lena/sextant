// model_test.go — unit tests for the agents Component. Drives the
// reducer directly with crafted messages; does NOT boot NATS. The model
// uses an agents.Bus interface so we can substitute a fake here.
package agents

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
	"github.com/love-lena/sextant/pkg/tui/component"
)

// fakeBus implements Bus for tests. Each method has knobs so individual
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

// Compile-time assertion that *Model satisfies component.Component.
// If the interface gains a method, this line breaks at build time.
var _ component.Component = (*Model)(nil)

func TestAgentsLoadedPopulatesList(t *testing.T) {
	bus := &fakeBus{rpcResp: sextantproto.ListAgentsResponse{
		Agents: []sextantproto.AgentSummary{summary("alpha", "running", "claude-coder")},
	}}
	m := New(Options{Bus: bus, Operator: "lena"})
	now := time.Now()
	got, _ := m.Update(agentsLoadedMsg{agents: bus.rpcResp.Agents, at: now})
	mm := got.(*Model)
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
	m := New(Options{Bus: &fakeBus{}, Operator: "lena"})
	got, _ := m.Update(agentsLoadedMsg{err: errors.New("boom"), at: time.Now()})
	mm := got.(*Model)
	if mm.errMsg == "" {
		t.Fatal("errMsg should be populated on RPC error")
	}
}

func TestArrowKeysMoveCursorWithinBounds(t *testing.T) {
	m := New(Options{Bus: &fakeBus{}, Operator: "lena"})
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
		mm := out.(*Model)
		if mm.cursor != tc.want {
			t.Fatalf("after %q: cursor = %d, want %d", tc.key, mm.cursor, tc.want)
		}
		m = mm
	}
}

func TestEnterTriggersPutKVWithSelectedAgentUUID(t *testing.T) {
	bus := &fakeBus{}
	m := New(Options{Bus: bus, Operator: "lena"})
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
	if got.bucket != UIStateBucket {
		t.Errorf("bucket = %q, want %q", got.bucket, UIStateBucket)
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
	m := New(Options{Bus: bus, Operator: "lena"})
	_, cmd := m.Update(specialKey("enter"))
	if cmd != nil {
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
	m := New(Options{Bus: bus, Operator: "lena"})
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
	m := New(Options{Bus: &fakeBus{}, Operator: "lena"})
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	mm := out.(*Model)
	if !mm.helpOpen {
		t.Fatal("? should open help")
	}
	out, _ = mm.Update(specialKey("esc"))
	mm = out.(*Model)
	if mm.helpOpen {
		t.Fatal("esc should close help")
	}
}

func TestSelectedAgentMsgUpdatesModel(t *testing.T) {
	m := New(Options{Bus: &fakeBus{}, Operator: "lena"})
	out, _ := m.Update(selectedAgentMsg{value: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"})
	mm := out.(*Model)
	if mm.selected != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("selected = %q", mm.selected)
	}
}

func TestPendingDeltaClampsAtZero(t *testing.T) {
	m := New(Options{Bus: &fakeBus{}, Operator: "lena"})
	out, _ := m.Update(pendingDeltaMsg{delta: 2})
	mm := out.(*Model)
	if mm.pending != 2 {
		t.Fatalf("pending = %d, want 2", mm.pending)
	}
	out, _ = mm.Update(pendingDeltaMsg{delta: -5})
	mm = out.(*Model)
	if mm.pending != 0 {
		t.Fatalf("pending = %d, want 0 (clamped)", mm.pending)
	}
}

func TestQuitKeyEmitsDoneMsg(t *testing.T) {
	m := New(Options{Bus: &fakeBus{}, Operator: "lena"})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q must emit a cmd")
	}
	// Component contract: q emits DoneMsg (host translates to tea.Quit).
	got := cmd()
	if _, ok := got.(component.DoneMsg); !ok {
		t.Fatalf("q cmd returned %T, want component.DoneMsg", got)
	}
}

func TestViewRendersWithoutPanic(t *testing.T) {
	m := New(Options{Bus: &fakeBus{}, Operator: "lena"})
	m.SetSize(100, 30)
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

// TestLoadMsgSeedsCursorOnNextLoad verifies that a LoadMsg arriving
// before agentsLoadedMsg lands seeds the cursor on the requested
// agent — the path `sextant agents show <id> -i` relies on.
func TestLoadMsgSeedsCursorOnNextLoad(t *testing.T) {
	bus := &fakeBus{}
	m := New(Options{Bus: bus, Operator: "lena"})
	target := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")

	// LoadMsg seeds initialSelectedID; agentsLoadedMsg should then move
	// the cursor onto that row.
	out, _ := m.Update(component.LoadMsg{ID: target.String()})
	m = out.(*Model)
	out, _ = m.Update(agentsLoadedMsg{
		agents: []sextantproto.AgentSummary{
			summaryWithUUID("a", uuid.New()),
			summaryWithUUID("b", target),
			summaryWithUUID("c", uuid.New()),
		},
		at: time.Now(),
	})
	mm := out.(*Model)
	if mm.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 (target seeded by LoadMsg)", mm.cursor)
	}
}

// TestFocusAndBlur verifies the Component focus contract.
func TestFocusAndBlur(t *testing.T) {
	m := New(Options{Bus: &fakeBus{}, Operator: "lena"})
	if m.Focused() {
		t.Fatal("new model should not be focused")
	}
	if cmd := m.Focus(); cmd != nil {
		t.Errorf("Focus() returned non-nil cmd; want nil for this model")
	}
	if !m.Focused() {
		t.Fatal("Focus() did not set focused bit")
	}
	m.Blur()
	if m.Focused() {
		t.Fatal("Blur() did not clear focused bit")
	}
}

// TestShortHelpAndFullHelpNonEmpty smoke-tests the help surfaces.
func TestShortHelpAndFullHelpNonEmpty(t *testing.T) {
	m := New(Options{Bus: &fakeBus{}, Operator: "lena"})
	if len(m.ShortHelp()) == 0 {
		t.Error("ShortHelp returned empty slice")
	}
	if len(m.FullHelp()) == 0 {
		t.Error("FullHelp returned empty slice")
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
