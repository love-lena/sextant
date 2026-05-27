package fixtures_test

import (
	"context"
	"testing"

	"github.com/love-lena/sextant/pkg/fixtures"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

func TestDemoFixtureRegistered(t *testing.T) {
	f, ok := fixtures.Get("demo")
	if !ok {
		t.Fatalf("demo fixture not registered; have %v", fixtures.Names())
	}
	if f.Name != "demo" {
		t.Errorf("Name = %q, want demo", f.Name)
	}
	if len(f.Agents) == 0 {
		t.Error("Demo fixture has no agents")
	}
	if len(f.Conversations) == 0 {
		t.Error("Demo fixture has no conversations")
	}
	if len(f.Pending) == 0 {
		t.Error("Demo fixture has no pending requests")
	}
}

func TestNamesIsSorted(t *testing.T) {
	got := fixtures.Names()
	if len(got) == 0 {
		t.Fatal("no fixtures registered")
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Errorf("Names() not sorted: %v", got)
			break
		}
	}
}

func TestBusServesListAgents(t *testing.T) {
	f := fixtures.MustGet("demo")
	bus := fixtures.NewBus(f)
	defer bus.Close() //nolint:errcheck // best-effort close

	var resp sextantproto.ListAgentsResponse
	if err := bus.RPC(context.Background(), rpc.VerbListAgents, sextantproto.ListAgentsRequest{}, &resp); err != nil {
		t.Fatalf("RPC list_agents: %v", err)
	}
	if len(resp.Agents) != len(f.Agents) {
		t.Errorf("agents len = %d, want %d", len(resp.Agents), len(f.Agents))
	}
	if resp.Agents[0].Name != "alice" {
		t.Errorf("agents[0].Name = %q, want alice", resp.Agents[0].Name)
	}
}

func TestBusFiltersByLifecycle(t *testing.T) {
	f := fixtures.MustGet("demo")
	bus := fixtures.NewBus(f)
	defer bus.Close() //nolint:errcheck

	var resp sextantproto.ListAgentsResponse
	req := sextantproto.ListAgentsRequest{
		Filter: &sextantproto.ListAgentsFilter{Lifecycle: string(sextantproto.LifecycleRunning)},
	}
	if err := bus.RPC(context.Background(), rpc.VerbListAgents, req, &resp); err != nil {
		t.Fatalf("RPC list_agents: %v", err)
	}
	for _, a := range resp.Agents {
		if a.Lifecycle != string(sextantproto.LifecycleRunning) {
			t.Errorf("filter leaked %q", a.Lifecycle)
		}
	}
}

func TestBusSubscribeFramesDeliversTranscript(t *testing.T) {
	f := fixtures.MustGet("demo")
	bus := fixtures.NewBus(f)
	defer bus.Close() //nolint:errcheck

	alice := fixtures.DemoAliceUUID()
	subject := "agents." + alice.String() + ".frames"
	ch, err := bus.Subscribe(context.Background(), subject)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	want := len(f.Conversations[alice])
	got := 0
drain:
	for got < want {
		select {
		case _, ok := <-ch:
			if !ok {
				break drain
			}
			got++
		default:
			break drain
		}
	}
	if got != want {
		t.Errorf("frames delivered = %d, want %d", got, want)
	}
}

func TestBusSubscribePendingEnvelopes(t *testing.T) {
	f := fixtures.MustGet("demo")
	bus := fixtures.NewBus(f)
	defer bus.Close() //nolint:errcheck

	ch, err := bus.Subscribe(context.Background(), "user_input.>")
	if err != nil {
		t.Fatalf("Subscribe user_input.>: %v", err)
	}
	got := 0
drain:
	for got < len(f.Pending) {
		select {
		case msg, ok := <-ch:
			if !ok {
				break drain
			}
			if msg.Envelope.Kind != sextantproto.KindUserInputRequest {
				t.Errorf("envelope kind = %q, want %q", msg.Envelope.Kind, sextantproto.KindUserInputRequest)
			}
			got++
		default:
			break drain
		}
	}
	if got != len(f.Pending) {
		t.Errorf("pending envelopes delivered = %d, want %d", got, len(f.Pending))
	}
}

func TestBusKVRoundTrip(t *testing.T) {
	bus := fixtures.NewBus(fixtures.MustGet("demo"))
	defer bus.Close() //nolint:errcheck

	if err := bus.PutKV(context.Background(), "ui_state", "lena.selected_agent", []byte("abc")); err != nil {
		t.Fatalf("PutKV: %v", err)
	}
	got, err := bus.GetKV(context.Background(), "ui_state", "lena.selected_agent")
	if err != nil {
		t.Fatalf("GetKV: %v", err)
	}
	if string(got) != "abc" {
		t.Errorf("GetKV = %q, want abc", string(got))
	}
}

func TestBusSendPromptRecords(t *testing.T) {
	bus := fixtures.NewBus(fixtures.MustGet("demo"))
	defer bus.Close() //nolint:errcheck

	alice := fixtures.DemoAliceUUID()
	if err := bus.SendPrompt(context.Background(), alice, "hello"); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}
	got := bus.SentPrompts()
	if len(got) != 1 || got[0].Text != "hello" || got[0].Agent != alice {
		t.Errorf("SentPrompts = %+v", got)
	}
}

func TestChatFramesAdapter(t *testing.T) {
	f := fixtures.MustGet("demo")
	frames := fixtures.ChatFrames(f, fixtures.DemoAliceUUID())
	if len(frames) == 0 {
		t.Fatal("ChatFrames returned empty for the demo alice agent")
	}
	if frames[0].Text == "" {
		t.Errorf("first frame text empty; want operator prompt")
	}
}
