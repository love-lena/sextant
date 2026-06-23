package violet

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/love-lena/sextant/protocol/wire"
)

// TestAnswerPreemptUnderLoad is the milestone bar (the bash spike failed it at
// 44→85s): an operator DM must be answered in a FEW SECONDS even while the bus
// floods violet with gate-bound events and a slow deep refresh is in flight.
//
// It exercises the real orchestrator with real concurrency — the three role
// goroutines, the bounded gate queue, the priority DM consumer — against a fake
// bus and a mock model server with realistic, role-specific latency:
//   - the gate turn is slow-ish (250ms) and there are MANY of them (a flood);
//   - the deep refresh is very slow (2s, sonnet);
//   - the conversational turn is fast (the warm answer).
//
// A single-loop design (the spike) would queue the answer behind the gate
// backlog + the deep turn and blow past several seconds. The answer-preempt
// design must land the answer regardless. We assert it lands well under the bar.
func TestAnswerPreemptUnderLoad(t *testing.T) {
	const (
		gateLatency   = 250 * time.Millisecond
		deepLatency   = 2 * time.Second
		answerLatency = 150 * time.Millisecond
		floodEvents   = 40
		bar           = 4 * time.Second // the "few seconds even under load" bar
	)

	srv := mockModelServer(t, map[string]time.Duration{
		"gate":           gateLatency,
		"conversational": answerLatency,
		"home-manager":   deepLatency,
	})
	defer srv.Close()

	bus := newFakeBus("01VIOLET", "01OPERATOR")
	v := New(bus, NewModelClient("test-key", srv.URL, srv.Client()), Config{
		OperatorID:     "01OPERATOR",
		SafetyInterval: time.Hour, // disable the safety tick; the gate drives wakes
		Logf:           func(string, ...any) {},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- v.Run(ctx) }()

	bus.waitSubscribed(t, 4) // DM + goals + artifact.> + crew

	// Flood the gate with significant-looking events (so they pass the pre-filter
	// and reach the slow gate model) — this is the busy bus.
	for i := 0; i < floodEvents; i++ {
		bus.deliver("msg.topic.crew", "01PEER", chatMessage("the brief is ready for review now"))
	}
	// Let a few gate turns + the deep refresh get genuinely in flight.
	time.Sleep(600 * time.Millisecond)

	// Sanity: the gate MUST still be backed up when the DM arrives — otherwise the
	// flood drained too fast and the test wouldn't prove preemption. With a 250ms
	// serial gate and a 40-event flood (~10s of work), only a handful can be done.
	if done := srv.callCount("gate"); done >= floodEvents {
		t.Fatalf("gate flood drained (%d/%d) before the DM — test does not exercise preemption", done, floodEvents)
	} else {
		t.Logf("gate still backed up at DM time: %d/%d events classified", done, floodEvents)
	}

	// Now the operator messages violet. Time how long until the reply is published.
	start := time.Now()
	bus.deliver(v.dmSubject, "01OPERATOR", chatMessage("where does v0.5 stand?"))

	reply, ok := bus.awaitPublish(v.dmSubject, bar+2*time.Second)
	elapsed := time.Since(start)
	if !ok {
		t.Fatalf("operator DM was never answered within %s (the answer-preempt bar failed)", bar+2*time.Second)
	}
	if elapsed > bar {
		t.Fatalf("answer took %s under load — exceeds the %s bar (the spike failure)", elapsed, bar)
	}
	t.Logf("answer-preempt under load: replied in %s (bar %s); flood=%d gate events @ %s, deep @ %s",
		elapsed.Round(time.Millisecond), bar, floodEvents, gateLatency, deepLatency)

	// The reply is a chat.message with text, ≤250 chars.
	var rec struct {
		Type string `json:"$type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(reply, &rec); err != nil {
		t.Fatalf("reply is not a chat.message: %v (%s)", err, reply)
	}
	if rec.Type != "chat.message" || rec.Text == "" {
		t.Fatalf("reply record = %+v", rec)
	}
	if len(rec.Text) > 250 {
		t.Fatalf("reply exceeds 250 chars: %d", len(rec.Text))
	}

	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestIgnoresOwnFrames asserts fix #5: violet never re-triggers on her own
// published reply (the self-loop). We deliver a frame authored by violet's own
// id and confirm it drives no gate turn / wake.
func TestIgnoresOwnFrames(t *testing.T) {
	srv := mockModelServer(t, map[string]time.Duration{"gate": 0, "conversational": 0, "home-manager": 0})
	defer srv.Close()
	bus := newFakeBus("01VIOLET", "01OPERATOR")
	v := New(bus, NewModelClient("k", srv.URL, srv.Client()), Config{OperatorID: "01OPERATOR", SafetyInterval: time.Hour, Logf: func(string, ...any) {}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go v.Run(ctx)
	bus.waitSubscribed(t, 4)

	// Drain the seed deep pass so the gate counter below starts from a clean base.
	srv.waitCalls("home-manager", 1, 2*time.Second)
	before := srv.callCount("gate")

	// A frame authored by violet herself, on a watched subject — must be ignored.
	bus.deliver("msg.topic.crew", "01VIOLET", chatMessage("the brief is ready for review"))
	time.Sleep(300 * time.Millisecond)
	if after := srv.callCount("gate"); after != before {
		t.Fatalf("own-authored frame triggered the gate: %d → %d (self-loop not prevented)", before, after)
	}
}

// TestScopedSubscriptionNotFirehose asserts fix #1: violet subscribes only to
// the scoped set, never msg.topic.> (the firehose).
func TestScopedSubscriptionNotFirehose(t *testing.T) {
	srv := mockModelServer(t, map[string]time.Duration{"home-manager": 0})
	defer srv.Close()
	bus := newFakeBus("01VIOLET", "01OPERATOR")
	v := New(bus, NewModelClient("k", srv.URL, srv.Client()), Config{OperatorID: "01OPERATOR", SafetyInterval: time.Hour, Logf: func(string, ...any) {}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go v.Run(ctx)
	bus.waitSubscribed(t, 4)

	subs := bus.subjects()
	for _, s := range subs {
		if s == "msg.topic.>" || s == "msg.>" {
			t.Fatalf("violet subscribed to the firehose %q (fix #1 violated)", s)
		}
	}
	want := map[string]bool{
		v.dmSubject:            true,
		"msg.topic.goals":      true,
		"msg.topic.artifact.>": true,
		"msg.topic.crew":       true,
	}
	for _, s := range subs {
		delete(want, s)
	}
	if len(want) != 0 {
		t.Fatalf("missing scoped subscriptions: %v (got %v)", want, subs)
	}
}

// TestDeepRefreshFreshensContextAndWritesHome proves the warm-context loop
// end-to-end: a deep pass gathers the real workspace (a review-flagged brief +
// a goal), the home-manager turn produces a snapshot that lands in the warm
// context, the curated `home` artifact is written, and a subsequent answer is
// drawn from that fresh context (not the placeholder).
func TestDeepRefreshFreshensContextAndWritesHome(t *testing.T) {
	srv := mockModelServer(t, map[string]time.Duration{"home-manager": 0, "conversational": 0})
	defer srv.Close()

	bus := newFakeBus("01VIOLET", "01OPERATOR")
	// Seed a review-flagged brief + a goal so the gather has real state.
	bus.artifacts["demo-brief"] = artifactValue{Name: "demo-brief", Revision: 2, Record: json.RawMessage(`{"title":"demo brief","review":{"state":"review"}}`)}
	bus.artifacts["goal.v0-5-0"] = artifactValue{Name: "goal.v0-5-0", Revision: 4, Record: json.RawMessage(`{"northstar":"v0.5.0","criteria":[{"id":"c1","text":"violet fast","status":"waiting-on-you"}]}`)}
	bus.rev = 4

	v := New(bus, NewModelClient("k", srv.URL, srv.Client()), Config{OperatorID: "01OPERATOR", SafetyInterval: time.Hour, Logf: func(string, ...any) {}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go v.Run(ctx)
	bus.waitSubscribed(t, 4)

	// The seed deep pass runs at startup. Wait for the home write.
	srv.waitCalls("home-manager", 1, 3*time.Second)
	deadline := time.Now().Add(2 * time.Second)
	var home artifactValue
	for time.Now().Before(deadline) {
		if h, err := bus.GetArtifact(ctx, "home"); err == nil {
			home = h
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if home.Name != "home" {
		t.Fatal("violet never wrote the curated home artifact after the deep pass")
	}
	// The curated home pins the review-flagged brief and carries a state line.
	if !strings.Contains(string(home.Record), "demo-brief") {
		t.Fatalf("home did not pin the review-flagged brief: %s", home.Record)
	}
	if !strings.Contains(string(home.Record), "real call") {
		t.Fatalf("home greeting note missing the curated state line: %s", home.Record)
	}

	// The warm context is now fresh (not the placeholder), so an answer draws from it.
	snap, gen := v.warm.get()
	if gen == 0 || strings.Contains(snap, "no workspace snapshot yet") {
		t.Fatalf("warm context not freshened by the deep pass: gen=%d snap=%q", gen, snap)
	}
}

// --- mock model server ---

// modelServer is a mock Anthropic Messages API. It keys per-role latency off the
// system prompt (each role's system text is distinctive) and counts calls, so a
// test can flood one role and time another. This exercises the REAL capture path
// and the REAL latency profile — the concurrency bar is not stubbed away.
type modelServer struct {
	*httptest.Server
	mu      sync.Mutex
	calls   map[string]int
	latency map[string]time.Duration
}

func mockModelServer(t *testing.T, latency map[string]time.Duration) *modelServer {
	t.Helper()
	ms := &modelServer{calls: map[string]int{}, latency: latency}
	ms.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		role := roleOf(body)
		ms.mu.Lock()
		ms.calls[role]++
		ms.mu.Unlock()
		if d := latency[role]; d > 0 {
			time.Sleep(d)
		}
		var reply string
		switch role {
		case "gate":
			reply = "WAKE"
		case "conversational":
			reply = "v0.5 is at its gate — one brief waits on you: [[demo-brief]]."
		default: // home-manager
			reply = "v0.5.0: dash redesign met; violet-fast in progress. One brief at its gate. crew idle."
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":`+jsonString(reply)+`}]}`)
	}))
	return ms
}

// roleOf identifies the role from the request's system prompt (each role's
// system text contains a distinctive marker).
func roleOf(body []byte) string {
	var req struct {
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
	}
	_ = json.Unmarshal(body, &req)
	var sys string
	if len(req.System) > 0 {
		sys = req.System[0].Text
	}
	switch {
	case strings.Contains(sys, "WAKE or SKIP"):
		return "gate"
	case strings.Contains(sys, "operator DM. Answer it"):
		return "conversational"
	default:
		return "home-manager"
	}
}

func (ms *modelServer) callCount(role string) int {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return ms.calls[role]
}

func (ms *modelServer) waitCalls(role string, n int, within time.Duration) {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if ms.callCount(role) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

var _ = wire.Epoch // keep wire imported for the shared record types
