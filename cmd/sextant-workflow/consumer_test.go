package main

// TestStartConsumer covers the workflow.start consumer (S2 slice):
//
//   (a) a workflow.start frame starts exactly one run and an ack status:ok
//       echoing the requestId + nonce.
//   (b) a duplicate frame id (replayed by DeliverAll) starts NO second run
//       (in-memory seen fence works).
//   (c) a bad record (empty prompt) is ignored (no ack).
//   (d) TIMING: the ok-ack arrives BEFORE the run completes (ack = accepted,
//       not done). This is the regression test for the ack-after-completion
//       bug: the old code ran `wfID, err := sc.runWorkflow(req)` then acked,
//       so the ack landed only after the coordinator finished — too late for
//       the dash's ~10s timeout in production. The new code acks immediately
//       after prepareWorkflow, then runs in a goroutine.
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
// workflow.start consumer: it asserts (a-d) per the file-level comment.
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
	// stepDelay is the deliberate pause the dispatcher inserts before completing
	// the step. The timing test asserts the ok-ack arrives well within this
	// window — i.e. before the run finishes.
	const stepDelay = 1500 * time.Millisecond
	stepTimeout := 10 * time.Second

	// ctx is an independent context for all shared bus clients and goroutines.
	// We deliberately do NOT derive it from t.Context(): Go's race detector flags
	// a race when a NATS delivery goroutine holds a context derived from
	// t.Context() while the testing framework writes to the *T during sub-test
	// teardown. A context.Background()-derived context avoids that and is safe
	// because t.Cleanup cancels it before the bus shuts down.
	ctx, cancelCtx := context.WithCancel(context.Background())
	t.Cleanup(cancelCtx)

	// runDone tracks outstanding background dispatcher goroutines so each
	// sub-test can drain them before it exits (prevents cross-sub-test pollution).
	var runDone sync.WaitGroup

	// --- Test-side dispatcher ---
	// Listens on the spawn subject; for every spawn.request it publishes a
	// spawn.ack, then waits stepDelay before publishing the step-done event.
	// The delay is intentional: it makes the timing test meaningful — the ack
	// must arrive before the delay elapses, proving ack-before-run.
	_, err = dispatcher.Subscribe(ctx, spawnSubj, func(m sextant.Message) {
		var req struct {
			Type   string `json:"$type"`
			Job    string `json:"job,omitempty"`
			Prompt string `json:"prompt,omitempty"`
		}
		if err := json.Unmarshal(m.Frame.Record, &req); err != nil || req.Type != typeSpawnRequest {
			return
		}

		// Publish spawn.ack immediately so the coordinator's ack-wait unblocks.
		ack := spawnAck{
			Type:      typeSpawnAck,
			ID:        "agent-test-" + m.Frame.ID[:8],
			RequestID: m.Frame.ID,
			Status:    "ok",
		}
		ackBytes, _ := json.Marshal(ack)
		if err := dispatcher.Publish(ctx, spawnSubj, json.RawMessage(ackBytes)); err != nil {
			return
		}

		// Delay before completing the step. The workflow.start.ack must arrive
		// before this sleep ends (the timing regression test).
		runDone.Add(1)
		go func() {
			defer runDone.Done()
			select {
			case <-time.After(stepDelay):
			case <-ctx.Done():
				return
			}
			evBytes := WorkflowEvent{Step: "run", Status: stepDone}.marshal()
			_ = dispatcher.Publish(ctx, eventsSubject(req.Job), evBytes)
		}()
	}, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("dispatcher Subscribe: %v", err)
	}

	// Start the consumer under the same background-derived context.
	consumerCtx, cancelConsumer := context.WithCancel(ctx)
	t.Cleanup(cancelConsumer)

	sc, sub, err := newStartConsumer(consumerCtx, consumer, spawnSubj, stepTimeout)
	if err != nil {
		t.Fatalf("newStartConsumer: %v", err)
	}
	t.Cleanup(sub.Stop)

	// Collect workflow.start.ack records published back on startSubject.
	type capturedAck struct {
		ack       WorkflowStartAck
		arrivedAt time.Time
	}
	var (
		ackMu   sync.Mutex
		ackList []capturedAck
		ackCond = sync.NewCond(&ackMu)
	)
	_, err = requester.Subscribe(ctx, startSubject, func(m sextant.Message) {
		var a WorkflowStartAck
		if err := json.Unmarshal(m.Frame.Record, &a); err != nil || a.Type != typeWorkflowStartAck {
			return
		}
		ackMu.Lock()
		ackList = append(ackList, capturedAck{ack: a, arrivedAt: time.Now()})
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

	resetAcks := func() {
		ackMu.Lock()
		ackList = nil
		ackMu.Unlock()
	}

	// --- (a) fresh workflow.start starts exactly one run + ack status:ok ---
	t.Run("fresh_start_gets_ok_ack", func(t *testing.T) {
		resetAcks()

		const wantNonce = "dash-nonce-abc123"
		rec := WorkflowStartRequest{
			Type:     typeWorkflowStart,
			Prompt:   "build something",
			Nonce:    wantNonce,
			Nickname: "tester",
		}
		recBytes, _ := json.Marshal(rec)
		out, err := requester.PublishMsg(ctx, startSubject, json.RawMessage(recBytes))
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

		// Drain background goroutines before the next sub-test.
		runDone.Wait()
	})

	// --- (b) duplicate frame id: fence prevents a second run ---
	t.Run("duplicate_frame_id_ignored", func(t *testing.T) {
		resetAcks()

		// Simulate DeliverAll replaying a frame by injecting the frame ID directly
		// into the consumer's seen map, then calling handle with that same ID.
		fakeID := "FAKEID-REPLAY-1234"
		sc.mu.Lock()
		sc.seen[fakeID] = true
		sc.mu.Unlock()

		req := WorkflowStartRequest{Type: typeWorkflowStart, Prompt: "replay"}
		recBytes, _ := json.Marshal(req)
		sc.handle(sextant.Message{
			Frame: wire.Frame{
				ID:     fakeID,
				Record: json.RawMessage(recBytes),
			},
		})

		got := waitForAcks(t, 1, 300*time.Millisecond)
		if len(got) != 0 {
			t.Errorf("fence failed: got %d ack(s) for duplicate frame id, want 0", len(got))
		}
	})

	// --- (c) empty prompt is ignored (no ack) ---
	t.Run("empty_prompt_ignored", func(t *testing.T) {
		resetAcks()

		bad := json.RawMessage(`{"$type":"workflow.start","prompt":""}`)
		if _, err := requester.PublishMsg(ctx, startSubject, bad); err != nil {
			t.Fatalf("PublishMsg bad record: %v", err)
		}
		notWF := json.RawMessage(`{"$type":"chat.message","text":"hi"}`)
		if _, err := requester.PublishMsg(ctx, startSubject, notWF); err != nil {
			t.Fatalf("PublishMsg wrong type: %v", err)
		}

		got := waitForAcks(t, 1, 300*time.Millisecond)
		if len(got) != 0 {
			t.Errorf("bad record not ignored: got %d ack(s), want 0", len(got))
		}
	})

	// --- (d) TIMING: ok-ack arrives BEFORE the run completes ---
	//
	// The dispatcher delays the step-done signal by stepDelay (1.5s). We assert
	// the ok-ack lands in well under half that window, proving ack-before-run.
	// This test FAILS on the old ack-after-completion code (ack arrives ~1.5s
	// late) and PASSES on the early-ack + goroutine-run fix.
	t.Run("ack_arrives_before_run_completes", func(t *testing.T) {
		resetAcks()

		rec := WorkflowStartRequest{
			Type:   typeWorkflowStart,
			Prompt: "timing-sensitive task",
			Nonce:  "timing-nonce",
		}
		recBytes, _ := json.Marshal(rec)
		publishedAt := time.Now()
		if _, err := requester.PublishMsg(ctx, startSubject, json.RawMessage(recBytes)); err != nil {
			t.Fatalf("PublishMsg: %v", err)
		}

		// The ack should arrive well before stepDelay elapses. Give it half the
		// step delay as a generous deadline; production expects it in < 1s.
		ackDeadline := stepDelay / 2
		acks := waitForAcks(t, 1, ackDeadline)
		if len(acks) != 1 {
			t.Fatalf("ack did not arrive within %s (want early ack, not ack-after-completion); got %d ack(s)", ackDeadline, len(acks))
		}
		a := acks[0].ack
		if a.Status != statusOK {
			t.Errorf("ack.status = %q, want %q (error: %s)", a.Status, statusOK, a.Error)
		}
		elapsed := acks[0].arrivedAt.Sub(publishedAt)
		if elapsed >= stepDelay {
			t.Errorf("ack arrived after run completion (elapsed %s >= stepDelay %s); ack-after-run bug NOT fixed", elapsed, stepDelay)
		}
		t.Logf("ack arrived %s after publish (stepDelay=%s) — ack-before-run confirmed", elapsed, stepDelay)

		// Drain the background run goroutine before teardown.
		runDone.Wait()
	})
}
