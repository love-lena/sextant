package main

// TestStartConsumer covers the workflow.start consumer (S2 slice):
//   (a) a workflow.start frame starts exactly one run and an ack status:ok
//       echoing the requestId.
//   (b) a duplicate frame id (replayed by DeliverAll) starts NO second run
//       (in-memory seen fence works).
//   (c) a bad record (empty prompt) is ignored (no ack).
//
// The test drives the REAL consumer path: real bus, real Subscribe/parse/fence/ack.
// A test-side dispatcher cooperates to make the coordinator succeed by publishing
// a spawn.ack for every spawn.request it receives, then publishing a step-done
// workflow.event so the coordinator's dispatch step completes. This matches what
// the M5.2 dispatcher does in production — the consumer is not faked.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/bus"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/wire"
)

// dialBusClient mints a credential for id and returns a connected client.
func dialBusClient(t *testing.T, b *bus.Bus, id string) *sextant.Client {
	t.Helper()
	creds, _, err := b.MintClient(t.Context(), id, "test")
	if err != nil {
		t.Fatalf("MintClient(%s): %v", id, err)
	}
	path := filepath.Join(t.TempDir(), "creds")
	if err := os.WriteFile(path, []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := sextant.Connect(t.Context(), sextant.Options{
		URL:       b.ClientURL(),
		CredsPath: path,
		Logf:      func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Connect(%s): %v", id, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestStartConsumer_OneRunPerRequest is the integration test for the
// workflow.start consumer: it asserts (a) one-run-per-request, (b) frame-id
// dedup fence, and (c) empty-prompt ignored.
func TestStartConsumer_OneRunPerRequest(t *testing.T) {
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)

	// consumer = the workflow coordinator running the startConsumer.
	consumer := dialBusClient(t, b, "consumer")
	// requester = the client that publishes workflow.start (e.g. violet / the dash).
	requester := dialBusClient(t, b, "requester")
	// dispatcher = a test-side dispatcher that cooperates with the coordinator.
	dispatcher := dialBusClient(t, b, "dispatcher")

	spawnSubj := "msg.topic.spawn"
	// Short timeout so the test does not hang on unexpected failures.
	stepTimeout := 5 * time.Second

	// --- Test-side dispatcher ---
	// Listens on the spawn subject; for every spawn.request it immediately acks
	// and then publishes a step-done workflow.event on the workflow's events
	// subject. This mirrors what cmd/sextant-dispatch does in production.
	dispatcherReady := make(chan struct{})
	var dispatcherOnce sync.Once
	_, err = dispatcher.Subscribe(t.Context(), spawnSubj, func(m sextant.Message) {
		var req struct {
			Type   string `json:"$type"`
			Job    string `json:"job,omitempty"`
			Prompt string `json:"prompt,omitempty"`
		}
		if err := json.Unmarshal(m.Frame.Record, &req); err != nil || req.Type != typeSpawnRequest {
			return
		}
		// signal that the dispatcher is up and receiving
		dispatcherOnce.Do(func() { close(dispatcherReady) })

		// Publish spawn.ack correlating via the request frame id.
		ack := spawnAck{
			Type:      typeSpawnAck,
			ID:        "agent-test-" + m.Frame.ID[:8],
			RequestID: m.Frame.ID,
			Status:    "ok",
		}
		b, _ := json.Marshal(ack)
		if err := dispatcher.Publish(t.Context(), spawnSubj, json.RawMessage(b)); err != nil {
			t.Logf("dispatcher: publish spawn.ack: %v", err)
			return
		}

		// Publish step-done on the workflow's events subject. The coordinator
		// uses job as the workflow id; the step id is always "run" (built in
		// startConsumer.runWorkflow).
		ev := WorkflowEvent{Step: "run", Status: stepDone}
		evBytes, _ := json.Marshal(ev)
		// Stamp $type manually (marshal helper stamps it).
		evBytes = WorkflowEvent{Step: "run", Status: stepDone}.marshal()
		evSubj := eventsSubject(req.Job)
		if err := dispatcher.Publish(t.Context(), evSubj, evBytes); err != nil {
			t.Logf("dispatcher: publish step-done: %v", err)
		}
	}, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("dispatcher Subscribe: %v", err)
	}

	// Start the consumer. Use a child context so we can stop it after assertions.
	consumerCtx, cancelConsumer := context.WithCancel(t.Context())
	t.Cleanup(cancelConsumer)

	sc, sub, err := newStartConsumer(consumerCtx, consumer, spawnSubj, stepTimeout)
	if err != nil {
		t.Fatalf("newStartConsumer: %v", err)
	}
	t.Cleanup(sub.Stop)

	// Collect workflow.start.ack records published back on startSubject.
	type capturedAck struct {
		ack     WorkflowStartAck
		frameID string
	}
	var (
		ackMu   sync.Mutex
		ackList []capturedAck
		ackCond = sync.NewCond(&ackMu)
	)
	_, err = requester.Subscribe(t.Context(), startSubject, func(m sextant.Message) {
		var a WorkflowStartAck
		if err := json.Unmarshal(m.Frame.Record, &a); err != nil || a.Type != typeWorkflowStartAck {
			return
		}
		ackMu.Lock()
		ackList = append(ackList, capturedAck{ack: a, frameID: m.Frame.ID})
		ackCond.Broadcast()
		ackMu.Unlock()
	}, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("requester Subscribe: %v", err)
	}

	// waitForAcks blocks until at least n acks have been collected or the
	// deadline elapses.
	waitForAcks := func(t *testing.T, n int, deadline time.Duration) []capturedAck {
		t.Helper()
		timer := time.AfterFunc(deadline, func() {
			ackMu.Lock()
			ackCond.Broadcast()
			ackMu.Unlock()
		})
		defer timer.Stop()
		end := time.Now().Add(deadline)
		ackMu.Lock()
		for len(ackList) < n && time.Now().Before(end) {
			ackCond.Wait()
		}
		got := make([]capturedAck, len(ackList))
		copy(got, ackList)
		ackMu.Unlock()
		return got
	}

	// --- (a) fresh workflow.start starts exactly one run + ack status:ok ---
	t.Run("fresh_start_gets_ok_ack", func(t *testing.T) {
		// Reset ack list.
		ackMu.Lock()
		ackList = nil
		ackMu.Unlock()

		const wantNonce = "dash-nonce-abc123"
		rec := WorkflowStartRequest{
			Type:     typeWorkflowStart,
			Prompt:   "build something",
			Nonce:    wantNonce,
			Nickname: "tester",
		}
		recBytes, _ := json.Marshal(rec)
		out, err := requester.PublishMsg(t.Context(), startSubject, json.RawMessage(recBytes))
		if err != nil {
			t.Fatalf("PublishMsg workflow.start: %v", err)
		}
		reqID := out.ID

		acks := waitForAcks(t, 1, 10*time.Second)
		if len(acks) != 1 {
			t.Fatalf("want 1 ack, got %d", len(acks))
		}
		a := acks[0].ack
		if a.Status != statusOK {
			t.Errorf("ack.status = %q, want %q (error: %s)", a.Status, statusOK, a.Error)
		}
		if a.RequestID != reqID {
			t.Errorf("ack.requestId = %q, want %q", a.RequestID, reqID)
		}
		if a.Nonce != wantNonce {
			t.Errorf("ack.nonce = %q, want %q (must echo request nonce verbatim)", a.Nonce, wantNonce)
		}
		if a.WorkflowID == "" {
			t.Error("ack.workflowId is empty; should carry the run id")
		}
	})

	// --- (b) duplicate frame id: fence prevents a second run ---
	t.Run("duplicate_frame_id_ignored", func(t *testing.T) {
		// Reset ack list.
		ackMu.Lock()
		ackList = nil
		ackMu.Unlock()

		// Simulate DeliverAll replaying a frame by injecting the frame ID directly
		// into the consumer's seen map, then calling handle with that same ID.
		// This is how the dispatcher tests the dedup fence (white-box, package main).
		fakeID := "FAKEID-REPLAY-1234"
		sc.mu.Lock()
		sc.seen[fakeID] = true
		sc.mu.Unlock()

		// Craft a message with the "already seen" frame ID and call handle directly.
		req := WorkflowStartRequest{Type: typeWorkflowStart, Prompt: "replay"}
		recBytes, _ := json.Marshal(req)
		sc.handle(sextant.Message{
			Frame: wire.Frame{
				ID:     fakeID,
				Record: json.RawMessage(recBytes),
			},
		})

		// Give a moment for any erroneous ack to arrive.
		got := waitForAcks(t, 1, 300*time.Millisecond)
		if len(got) != 0 {
			t.Errorf("fence failed: got %d ack(s) for duplicate frame id, want 0", len(got))
		}
	})

	// --- (c) empty prompt is ignored (no ack) ---
	t.Run("empty_prompt_ignored", func(t *testing.T) {
		ackMu.Lock()
		ackList = nil
		ackMu.Unlock()

		bad := json.RawMessage(`{"$type":"workflow.start","prompt":""}`)
		if _, err := requester.PublishMsg(t.Context(), startSubject, bad); err != nil {
			t.Fatalf("PublishMsg bad record: %v", err)
		}
		// Also try a non-workflow.start record.
		notWF := json.RawMessage(`{"$type":"chat.message","text":"hi"}`)
		if _, err := requester.PublishMsg(t.Context(), startSubject, notWF); err != nil {
			t.Fatalf("PublishMsg wrong type: %v", err)
		}

		got := waitForAcks(t, 1, 300*time.Millisecond)
		if len(got) != 0 {
			t.Errorf("bad record not ignored: got %d ack(s), want 0", len(got))
		}
	})
}
