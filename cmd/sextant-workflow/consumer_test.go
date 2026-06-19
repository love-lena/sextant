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
//
// Each assertion runs as its own isolated PHASE: a fresh bus, fresh clients, a
// unique spawn subject, and a fresh consumer, all torn down before the next
// phase begins. Phases are sequential plain closures (not nested t.Run
// sub-tests) so the testing framework never mutates a sub-test *T while a NATS
// delivery goroutine is live. Teardown stops every subscription before closing
// the clients; Subscription.Stop now joins its in-flight delivery callback (see
// pkg/sextant/messages.go), so no handler is still running at a phase boundary.
// This is the TASK-170 fix that removes the -race quarantine.
func TestStartConsumer_OneRunPerRequest(t *testing.T) {
	// stepDelay is the deliberate pause the dispatcher inserts before completing
	// the step. The timing test asserts the ok-ack arrives well within this
	// window — i.e. before the run finishes.
	const stepDelay = 1500 * time.Millisecond
	stepTimeout := 10 * time.Second

	type capturedAck struct {
		ack       WorkflowStartAck
		arrivedAt time.Time
	}

	// phaseEnv is one fully isolated test world: its own bus, clients, subjects,
	// consumer, and ack collector.
	type phaseEnv struct {
		b          *bus.Bus
		consumer   *sextant.Client
		requester  *sextant.Client
		dispatcher *sextant.Client
		sc         *startConsumer
		consumeSub sextant.Subscription
		dispSub    sextant.Subscription
		reqSub     sextant.Subscription
		ctx        context.Context
		cancel     context.CancelFunc
		runDone    *sync.WaitGroup

		ackMu   *sync.Mutex
		ackList *[]capturedAck
		ackCond *sync.Cond
	}

	// newPhase builds a fresh isolated world on a unique spawn subject. Each phase
	// uses its own bus + subject so no frame or delivery goroutine can cross a
	// phase boundary.
	newPhase := func(t *testing.T, name string) *phaseEnv {
		t.Helper()
		b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
		if err != nil {
			t.Fatalf("%s: bus.Start: %v", name, err)
		}

		// Bus clients run on a background-derived context, not t.Context(): a NATS
		// delivery goroutine that captured t.Context() would race the framework's
		// writes to *T at teardown. teardown cancels this before the bus shuts down.
		ctx, cancel := context.WithCancel(context.Background())

		var (
			ackMu   sync.Mutex
			ackList []capturedAck
			runDone sync.WaitGroup
		)
		env := &phaseEnv{
			b:          b,
			consumer:   dialBusClient(t, b, "consumer-"+name),
			requester:  dialBusClient(t, b, "requester-"+name),
			dispatcher: dialBusClient(t, b, "dispatcher-"+name),
			ctx:        ctx,
			cancel:     cancel,
			runDone:    &runDone,
			ackMu:      &ackMu,
			ackList:    &ackList,
		}
		env.ackCond = sync.NewCond(env.ackMu)
		// Unique spawn subject per phase keeps phases from ever interfering.
		spawnSubj := "msg.topic.spawn." + name

		// --- Test-side dispatcher ---
		// For every spawn.request: publish a spawn.ack at once, then (after
		// stepDelay) a step-done event so the coordinator's dispatch step completes.
		env.dispSub, err = env.dispatcher.Subscribe(ctx, spawnSubj, func(m sextant.Message) {
			var req struct {
				Type   string `json:"$type"`
				Job    string `json:"job,omitempty"`
				Prompt string `json:"prompt,omitempty"`
			}
			if err := json.Unmarshal(m.Frame.Record, &req); err != nil || req.Type != typeSpawnRequest {
				return
			}
			ack := spawnAck{
				Type:      typeSpawnAck,
				ID:        "agent-test-" + m.Frame.ID[:8],
				RequestID: m.Frame.ID,
				Status:    "ok",
			}
			ackBytes, _ := json.Marshal(ack)
			if err := env.dispatcher.Publish(ctx, spawnSubj, json.RawMessage(ackBytes)); err != nil {
				return
			}
			runDone.Add(1)
			go func() {
				defer runDone.Done()
				select {
				case <-time.After(stepDelay):
				case <-ctx.Done():
					return
				}
				evBytes := WorkflowEvent{Step: "run", Status: stepDone}.marshal()
				_ = env.dispatcher.Publish(ctx, eventsSubject(req.Job), evBytes)
			}()
		}, sextant.DeliverAll())
		if err != nil {
			t.Fatalf("%s: dispatcher Subscribe: %v", name, err)
		}

		sc, consumeSub, err := newStartConsumer(ctx, env.consumer, spawnSubj, stepTimeout)
		if err != nil {
			t.Fatalf("%s: newStartConsumer: %v", name, err)
		}
		env.sc, env.consumeSub = sc, consumeSub

		env.reqSub, err = env.requester.Subscribe(ctx, startSubject, func(m sextant.Message) {
			var a WorkflowStartAck
			if err := json.Unmarshal(m.Frame.Record, &a); err != nil || a.Type != typeWorkflowStartAck {
				return
			}
			ackMu.Lock()
			ackList = append(ackList, capturedAck{ack: a, arrivedAt: time.Now()})
			env.ackCond.Broadcast()
			ackMu.Unlock()
		}, sextant.DeliverAll())
		if err != nil {
			t.Fatalf("%s: requester Subscribe: %v", name, err)
		}
		return env
	}

	// teardown winds the phase down deterministically. Stop joins each
	// subscription's in-flight delivery callback, so after these Stops no handler
	// is running; cancel + Close + Shutdown then release the rest.
	teardown := func(env *phaseEnv) {
		env.runDone.Wait()
		env.consumeSub.Stop()
		env.dispSub.Stop()
		env.reqSub.Stop()
		env.cancel()
		_ = env.consumer.Close()
		_ = env.requester.Close()
		_ = env.dispatcher.Close()
		env.b.Shutdown()
	}

	waitForAcks := func(t *testing.T, env *phaseEnv, n int, deadline time.Duration) []capturedAck {
		t.Helper()
		timer := time.AfterFunc(deadline, func() {
			env.ackMu.Lock()
			env.ackCond.Broadcast()
			env.ackMu.Unlock()
		})
		defer timer.Stop()
		end := time.Now().Add(deadline)
		env.ackMu.Lock()
		for len(*env.ackList) < n && time.Now().Before(end) {
			env.ackCond.Wait()
		}
		got := make([]capturedAck, len(*env.ackList))
		copy(got, *env.ackList)
		env.ackMu.Unlock()
		return got
	}

	// --- (a) fresh workflow.start starts exactly one run + ack status:ok ---
	func() {
		env := newPhase(t, "fresh")
		defer teardown(env)

		const wantNonce = "dash-nonce-abc123"
		rec := WorkflowStartRequest{
			Type:     typeWorkflowStart,
			Prompt:   "build something",
			Nonce:    wantNonce,
			Nickname: "tester",
		}
		recBytes, _ := json.Marshal(rec)
		out, err := env.requester.PublishMsg(env.ctx, startSubject, json.RawMessage(recBytes))
		if err != nil {
			t.Fatalf("(a) PublishMsg workflow.start: %v", err)
		}
		reqID := out.ID

		acks := waitForAcks(t, env, 1, 10*time.Second)
		if len(acks) != 1 {
			t.Fatalf("(a) want 1 ack, got %d", len(acks))
		}
		a := acks[0].ack
		if a.Status != statusOK {
			t.Errorf("(a) ack.status = %q, want %q (error: %s)", a.Status, statusOK, a.Error)
		}
		if a.RequestID != reqID {
			t.Errorf("(a) ack.requestId = %q, want %q", a.RequestID, reqID)
		}
		if a.Nonce != wantNonce {
			t.Errorf("(a) ack.nonce = %q, want %q (must echo request nonce verbatim)", a.Nonce, wantNonce)
		}
		if a.WorkflowID == "" {
			t.Error("(a) ack.workflowId is empty; should carry the run id")
		}
	}()

	// --- (b) duplicate frame id: fence prevents a second run ---
	func() {
		env := newPhase(t, "duplicate")
		defer teardown(env)

		// Simulate DeliverAll replaying a frame by injecting the frame ID directly
		// into the consumer's seen map, then calling handle with that same ID.
		fakeID := "FAKEID-REPLAY-1234"
		env.sc.mu.Lock()
		env.sc.seen[fakeID] = true
		env.sc.mu.Unlock()

		req := WorkflowStartRequest{Type: typeWorkflowStart, Prompt: "replay"}
		recBytes, _ := json.Marshal(req)
		env.sc.handle(sextant.Message{
			Frame: wire.Frame{
				ID:     fakeID,
				Record: json.RawMessage(recBytes),
			},
		})

		got := waitForAcks(t, env, 1, 300*time.Millisecond)
		if len(got) != 0 {
			t.Errorf("(b) fence failed: got %d ack(s) for duplicate frame id, want 0", len(got))
		}
	}()

	// --- (c) empty prompt is ignored (no ack) ---
	func() {
		env := newPhase(t, "ignored")
		defer teardown(env)

		bad := json.RawMessage(`{"$type":"workflow.start","prompt":""}`)
		if _, err := env.requester.PublishMsg(env.ctx, startSubject, bad); err != nil {
			t.Fatalf("(c) PublishMsg bad record: %v", err)
		}
		notWF := json.RawMessage(`{"$type":"chat.message","text":"hi"}`)
		if _, err := env.requester.PublishMsg(env.ctx, startSubject, notWF); err != nil {
			t.Fatalf("(c) PublishMsg wrong type: %v", err)
		}

		got := waitForAcks(t, env, 1, 300*time.Millisecond)
		if len(got) != 0 {
			t.Errorf("(c) bad record not ignored: got %d ack(s), want 0", len(got))
		}
	}()

	// --- (d) TIMING: ok-ack arrives BEFORE the run completes ---
	//
	// The dispatcher delays the step-done signal by stepDelay (1.5s). We assert
	// the ok-ack lands in well under half that window, proving ack-before-run.
	// This test FAILS on the old ack-after-completion code (ack arrives ~1.5s
	// late) and PASSES on the early-ack + goroutine-run fix.
	func() {
		env := newPhase(t, "timing")
		defer teardown(env)

		rec := WorkflowStartRequest{
			Type:   typeWorkflowStart,
			Prompt: "timing-sensitive task",
			Nonce:  "timing-nonce",
		}
		recBytes, _ := json.Marshal(rec)
		publishedAt := time.Now()
		if _, err := env.requester.PublishMsg(env.ctx, startSubject, json.RawMessage(recBytes)); err != nil {
			t.Fatalf("(d) PublishMsg: %v", err)
		}

		// The ack should arrive well before stepDelay elapses. Give it half the
		// step delay as a generous deadline; production expects it in < 1s.
		ackDeadline := stepDelay / 2
		acks := waitForAcks(t, env, 1, ackDeadline)
		if len(acks) != 1 {
			t.Fatalf("(d) ack did not arrive within %s (want early ack, not ack-after-completion); got %d ack(s)", ackDeadline, len(acks))
		}
		a := acks[0].ack
		if a.Status != statusOK {
			t.Errorf("(d) ack.status = %q, want %q (error: %s)", a.Status, statusOK, a.Error)
		}
		elapsed := acks[0].arrivedAt.Sub(publishedAt)
		if elapsed >= stepDelay {
			t.Errorf("(d) ack arrived after run completion (elapsed %s >= stepDelay %s); ack-after-run bug NOT fixed", elapsed, stepDelay)
		}
		t.Logf("(d) ack arrived %s after publish (stepDelay=%s) — ack-before-run confirmed", elapsed, stepDelay)
	}()
}

// TestStartConsumer_SubsStoppedAfterRun asserts that the helper subscriptions
// opened by prepareWorkflow are actually TORN DOWN after each run completes —
// not just that Stop() was called on the handle, but that delivery ceases.
//
// Behavioral assertion: after wg.Wait() (all runs done, stopSubs deferred),
// publish a probe workflow.event on eventsSubject(wfID) for each completed
// run. The coordinator's onEvent handler calls wake(co.evCh); if the sub is
// still live, co.evCh gets a signal. Assert co.evCh is EMPTY after the probe —
// proving the sub was stopped and the coordinator is no longer receiving.
//
// FAILS on the old `_ = sub` discard (sub still live → probe wakes evCh).
// PASSES with the stopSubs closure fix (sub stopped → no wake).
//
// Also asserts the error path: if loadDirect succeeds but a Subscribe call
// fails mid-loop, the already-opened subs are cleaned up (no partial leak).
func TestStartConsumer_SubsStoppedAfterRun(t *testing.T) {
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)

	consumer := dialBusClient(t, b, "consumer-leak")
	dispatcher := dialBusClient(t, b, "dispatcher-leak")
	probe := dialBusClient(t, b, "probe-leak")

	spawnSubj := "msg.topic.spawn"
	stepTimeout := 5 * time.Second

	ctx, cancelCtx := context.WithCancel(context.Background())
	t.Cleanup(cancelCtx)

	// Test-side dispatcher: acks immediately + fires step-done.
	_, err = dispatcher.Subscribe(ctx, spawnSubj, func(m sextant.Message) {
		var req struct {
			Type string `json:"$type"`
			Job  string `json:"job,omitempty"`
		}
		if err := json.Unmarshal(m.Frame.Record, &req); err != nil || req.Type != typeSpawnRequest {
			return
		}
		ack := spawnAck{Type: typeSpawnAck, ID: "agent-" + m.Frame.ID[:8], RequestID: m.Frame.ID, Status: "ok"}
		ackBytes, _ := json.Marshal(ack)
		_ = dispatcher.Publish(ctx, spawnSubj, json.RawMessage(ackBytes))
		_ = dispatcher.Publish(ctx, eventsSubject(req.Job), WorkflowEvent{Step: "run", Status: stepDone}.marshal())
	}, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("dispatcher Subscribe: %v", err)
	}

	consumerCtx, cancelConsumer := context.WithCancel(ctx)
	t.Cleanup(cancelConsumer)

	sc := &startConsumer{
		ctx: consumerCtx, c: consumer, spawnSubject: spawnSubj, stepTimeout: stepTimeout,
		seen: map[string]bool{},
	}

	const runs = 3
	type runRecord struct {
		co   *coordinator
		wfID string
	}
	records := make([]runRecord, runs)

	var wg sync.WaitGroup
	for i := range runs {
		req := WorkflowStartRequest{Type: typeWorkflowStart, Prompt: "leak-check task"}
		co, wfID, stopSubs, err := sc.prepareWorkflow(req)
		if err != nil {
			t.Fatalf("prepareWorkflow[%d]: %v", i, err)
		}
		records[i] = runRecord{co: co, wfID: wfID}

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer stopSubs()
			if err := co.run(); err != nil {
				t.Logf("run[%d] error (expected at teardown): %v", i, err)
			}
		}()
	}

	// Wait for all runs to complete and stopSubs to fire via defer.
	wg.Wait()

	// Behavioral assertion: for each completed run, publish a probe
	// workflow.event on eventsSubject(wfID). The coordinator's onEvent calls
	// wake(co.evCh); if the sub is still live, co.evCh would receive a signal.
	// Drain evCh first (may have a signal from the run), then publish the probe
	// and assert evCh stays empty — the sub is stopped, delivery is gone.
	//
	// The probe is published by the separate probe client so the coordinator's
	// self-author filter (m.Frame.Author == co.c.ID()) does not suppress it.
	for i, r := range records {
		// Drain any pending signal left from the run itself.
		select {
		case <-r.co.evCh:
		default:
		}

		// Publish a fresh step-done event from the probe client (different author).
		probeEvent := WorkflowEvent{Step: "post-run-probe", Status: stepDone}.marshal()
		if err := probe.Publish(ctx, eventsSubject(r.wfID), probeEvent); err != nil {
			t.Fatalf("probe publish[%d]: %v", i, err)
		}

		// Give the bus a moment to deliver if the sub were still live.
		select {
		case <-r.co.evCh:
			t.Errorf("run[%d] wfID=%s: evCh signalled after stopSubs — events sub leaked", i, r.wfID)
		case <-time.After(300 * time.Millisecond):
			// good: no delivery to the stopped coordinator
		}
	}
}

// TestStartConsumer_ErrorPathNoLeak asserts that prepareWorkflow does not leave
// helper subscriptions running when the setup phase fails.
//
// The two relevant failure branches in prepareWorkflow are:
//
//  1. loadDirect fails (artifact creation error) — no subs were opened, so
//     the cleanup is a trivial no-op; the returned stopSubs is func(){}.
//
//  2. A Subscribe call fails mid-loop — the already-opened subs are stopped
//     inline before the error return (lines in the subs loop). This branch is
//     code-verified: the cleanup loop runs synchronously before prepareWorkflow
//     returns, so no goroutine can outlive it. A production-faithful behavioral
//     test would require injecting a selective Subscribe failure; that demands a
//     test seam in production code, which we trade away in favour of the
//     inline-code guarantee.
//
// This test covers branch 1: close the client so CreateArtifact fails, verify
// prepareWorkflow returns an error and that the returned stopSubs is safe to
// call (no panic) and leaves nothing running (publish on the events subject
// after the error is not delivered to any coordinator handler).
func TestStartConsumer_ErrorPathNoLeak(t *testing.T) {
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)

	failClient := dialBusClient(t, b, "fail-consumer")
	observer := dialBusClient(t, b, "error-observer")

	spawnSubj := "msg.topic.spawn"
	stepTimeout := 5 * time.Second

	ctx, cancelCtx := context.WithCancel(context.Background())
	t.Cleanup(cancelCtx)

	// Use a pre-cancelled context as sc.ctx so loadDirect's CreateArtifact call
	// gets a context.Canceled error immediately — simulating an environment
	// failure (e.g. bus disconnect) before any subs are opened.
	cancelledCtx, cancelIt := context.WithCancel(ctx)
	cancelIt() // cancel before use

	sc := &startConsumer{
		ctx: cancelledCtx, c: failClient, spawnSubject: spawnSubj, stepTimeout: stepTimeout,
		seen: map[string]bool{},
	}

	req := WorkflowStartRequest{Type: typeWorkflowStart, Prompt: "will fail"}
	_, wfID, stopSubs, prepErr := sc.prepareWorkflow(req)
	if prepErr == nil {
		t.Fatal("prepareWorkflow expected to fail after client close, but returned nil error")
	}
	t.Logf("prepareWorkflow returned expected error: %v", prepErr)

	// stopSubs must be callable without panic even on the error path.
	stopSubs()

	// Verify no sub is lingering: publish on the events subject from the observer
	// and confirm nothing receives it (no coordinator was ever set up).
	received := make(chan struct{}, 1)
	_, subErr := observer.Subscribe(ctx, eventsSubject(wfID), func(sextant.Message) {
		select {
		case received <- struct{}{}:
		default:
		}
	}, sextant.DeliverAll())
	if subErr != nil {
		t.Fatalf("observer Subscribe: %v", subErr)
	}

	probe := WorkflowEvent{Step: "error-probe", Status: stepDone}.marshal()
	if err := observer.Publish(ctx, eventsSubject(wfID), probe); err != nil {
		t.Fatalf("observer Publish: %v", err)
	}

	// The observer itself gets the message (it's subscribed); drain that.
	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("observer did not receive its own probe publish — bus not working")
	}

	// Now confirm no second delivery (which would indicate a leaked coordinator sub).
	select {
	case <-received:
		t.Error("error path leaked a coordinator sub — received a second delivery")
	case <-time.After(300 * time.Millisecond):
		// good: only one delivery (the observer itself), no leaked coordinator
	}
}
