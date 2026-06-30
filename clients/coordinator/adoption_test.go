package main

// Durable run.start adoption integration tests (TASK-259) over a REAL bus.
//
// The live failure these guard: a run.start published while no coordinator is subscribed
// (the coordinator down, restarting, or busy) was LOST under New-only delivery — the run
// stalled forever at owner=none. The fix is DeliverAll on the start subject plus an
// idempotent, single-writer-CAS adoption guard (shouldAdopt + claimOwnership). These tests
// drive the REAL consumer path (newStartConsumer → handle → prepareRun → adopt → walk) and
// assert the property, not one happy path:
//
//   - TestAdopt_PublishBeforeSubscribe (AC#2): a start published BEFORE any coordinator
//     subscribes is adopted on (re)subscribe — the start is not lost. The fake-pass guard
//     the ticket calls out: a test that publishes AFTER subscribe does not cover this; this
//     test publishes first, then subscribes.
//   - TestAdopt_ReadoptsAfterOwnerCrash (AC#3): a coordinator adopts, then its process
//     "crashes" (subscription stopped + client closed → owner offline); a fresh coordinator
//     replays the start and RE-ADOPTS the orphaned run, driving it to terminal — never left
//     owner=<dead> and stalled.
//   - TestAdopt_IdempotentReplayDoesNotRerun (regression the New-only choice was avoiding):
//     a start replayed for an already-DONE run is a no-op — no second dispatch, no owner
//     clobber, no status change. This is the TASK-192 crash-loop the durable mechanism must
//     not reintroduce.
//   - TestAdopt_StaleStartNoEnvelopeSkipped (TASK-192 stale class): a replayed start whose
//     run artifact never existed is skipped quietly (no ack, no crash-loop), the case the
//     old New-only behaviour handled by ignoring all history.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/conventions/workflow/go"
	"github.com/love-lena/sextant/sdk/go"
)

// startConsumerAs subscribes a real listen-mode start consumer on consumer and returns a
// stop func that ends only its run.start subscription (modelling the consumer going away
// without tearing the whole bus down). Repo-less (empty store): no worktree provisioning.
func startConsumerAs(t *testing.T, ctx context.Context, consumer *sextant.Client, spawnSubj string, stepTimeout time.Duration) func() {
	t.Helper()
	_, sub, err := newStartConsumer(ctx, consumer, spawnSubj, stepTimeout, "")
	if err != nil {
		t.Fatalf("newStartConsumer: %v", err)
	}
	return sub.Stop
}

// collectStartAcks subscribes (DeliverAll) to RunStartSubject and records every
// run.start.ack, returning a snapshot accessor. Used to assert acks fire (a real adoption)
// or do NOT fire (a stale start skipped).
func collectStartAcks(t *testing.T, ctx context.Context, c *sextant.Client) func() []workflow.RunStartAck {
	t.Helper()
	var (
		mu   sync.Mutex
		acks []workflow.RunStartAck
	)
	if _, err := c.Subscribe(ctx, workflow.RunStartSubject, func(m sextant.Message) {
		var a workflow.RunStartAck
		if err := json.Unmarshal(m.Frame.Record, &a); err != nil || a.Type != workflow.TypeRunStartAck {
			return
		}
		mu.Lock()
		acks = append(acks, a)
		mu.Unlock()
	}, sextant.DeliverAll()); err != nil {
		t.Fatalf("subscribe acks: %v", err)
	}
	return func() []workflow.RunStartAck {
		mu.Lock()
		defer mu.Unlock()
		out := make([]workflow.RunStartAck, len(acks))
		copy(out, acks)
		return out
	}
}

// TestAdopt_PublishBeforeSubscribe is the AC#2 core property: a run.start published while
// NO coordinator is subscribed (the coordinator down / restarting / not yet up) must be
// adopted once a coordinator subscribes — the start is not lost. The run artifact exists
// (the dash's spawn act), and the start is published BEFORE startConsumerAs runs. Under the
// old New-only delivery this run would stall forever at owner=none; under DeliverAll the
// replay delivers the retained start on subscribe and the run is adopted and driven to done.
func TestAdopt_PublishBeforeSubscribe(t *testing.T) {
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	requester := dialBusClient(t, b, "requester")
	dispatcher := dialBusClient(t, b, "dispatcher")
	consumer := dialBusClient(t, b, "consumer")
	spawnSubj := "msg.topic.spawn"

	cooperatingDispatcher(t, ctx, dispatcher, spawnSubj, 0)

	run := workflow.Run{
		ID: "01PUBFIRST000000000000000A", Status: workflow.RunRunning, Objective: "adopt me late",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "work", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	// Publish the start BEFORE any coordinator subscribes — the property under test. (No
	// consumer is listening here, so a New-only subscription would never see this frame.)
	writeRunAndStart(t, ctx, requester, run, "")

	// Now bring the coordinator up. DeliverAll must replay the retained start.
	startConsumerAs(t, ctx, consumer, spawnSubj, 10*time.Second)

	got := pollRun(t, ctx, requester, run.ID, 15*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Fatalf("a start published before the coordinator subscribed was not adopted/driven: status=%q (want done) — the start was lost", got.Status)
	}
	if got.Owner == "" {
		t.Errorf("run adopted but owner is empty; owner must be set on adoption")
	}
}

// TestAdopt_ReadoptsAfterOwnerCrash is the AC#3 property: a coordinator PROCESS crash must
// not leave a run permanently unadopted or stalled. Coordinator A adopts a run that parks
// at a checkpoint (so it is mid-run, not finished), then A "crashes": its start
// subscription is stopped AND its client is closed, so its owner id goes OFFLINE in the bus
// directory. A fresh coordinator B subscribes; DeliverAll replays the start; shouldAdopt
// sees the run owned by the now-offline A and RE-ADOPTS it (a live owner would have been
// skipped). B drives the run to terminal. The run never stalls at a dead owner.
func TestAdopt_ReadoptsAfterOwnerCrash(t *testing.T) {
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	requester := dialBusClient(t, b, "requester")
	dispatcher := dialBusClient(t, b, "dispatcher")
	spawnSubj := "msg.topic.spawn"
	cooperatingDispatcher(t, ctx, dispatcher, spawnSubj, 0)

	// A checkpoint step parks the run at waiting after A adopts, so the run is in-flight
	// (owned, non-terminal) when A crashes — the orphaned-run condition.
	run := workflow.Run{
		ID: "01CRASHRE000000000000000AB", Status: workflow.RunRunning, Objective: "survive a crash",
		Steps: []workflow.RunStep{
			{ID: "gate", Label: "approve me", Kind: workflow.KindCheckpoint, Status: workflow.StepUpcoming},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	// Coordinator A: its own client identity (so closing it takes A offline).
	coordA := dialBusClientNoClose(t, b, "coord-A")
	stopA := startConsumerAs(t, ctx, coordA, spawnSubj, 10*time.Second)

	// A adopts and parks at the checkpoint. Capture the owner it claimed.
	parked := pollRun(t, ctx, requester, run.ID, 10*time.Second, func(r workflow.Run) bool {
		return r.Status == workflow.RunWaiting && r.Owner != ""
	})
	ownerA := parked.Owner
	if ownerA != coordA.ID() {
		t.Fatalf("owner = %q, want coord-A %q", ownerA, coordA.ID())
	}

	// CRASH A: stop its subscription and close its client so its id goes offline.
	stopA()
	if err := coordA.Close(); err != nil {
		t.Fatalf("close coord-A: %v", err)
	}
	// Wait for the bus to observe A offline (presence is connection-derived).
	waitOffline(t, ctx, requester, ownerA)

	// Coordinator B: a fresh process. DeliverAll replays the start; shouldAdopt sees the
	// run owned by the now-offline A and re-adopts.
	coordB := dialBusClient(t, b, "coord-B")
	startConsumerAs(t, ctx, coordB, spawnSubj, 10*time.Second)

	// B must re-own the run (owner flips to B), proving re-adoption — never stuck at dead A.
	reowned := pollRun(t, ctx, requester, run.ID, 15*time.Second, func(r workflow.Run) bool {
		return r.Owner == coordB.ID()
	})
	if reowned.Owner != coordB.ID() {
		t.Fatalf("run not re-adopted after owner crash: owner=%q (want coord-B %q) — stranded at the dead owner", reowned.Owner, coordB.ID())
	}

	// And the run must make progress to terminal under B (approve the checkpoint).
	if err := requester.Publish(ctx, workflow.RunControlSubject(run.ID),
		(workflow.RunControl{Verb: workflow.CtlApprove}).Marshal()); err != nil {
		t.Fatalf("publish approve: %v", err)
	}
	done := pollRun(t, ctx, requester, run.ID, 15*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if done.Status != workflow.RunDone {
		t.Errorf("re-adopted run final status = %q, want done", done.Status)
	}
}

// TestAdopt_IdempotentReplayDoesNotRerun is the regression the New-only choice was avoiding
// (TASK-192): a durable start subject must not re-run a finished run on replay. A run is
// driven to done by coordinator A; then a fresh coordinator B subscribes and DeliverAll
// replays the same retained start. shouldAdopt sees the run is already terminal and SKIPS:
// no second dispatch (the dispatcher sees no new spawn.request), no owner clobber, no status
// change. This is the idempotency the durable mechanism must provide.
func TestAdopt_IdempotentReplayDoesNotRerun(t *testing.T) {
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	requester := dialBusClient(t, b, "requester")
	spawnSubj := "msg.topic.spawn"

	// Count spawn.requests so a replay-driven re-run would be visible as extra dispatches.
	var (
		spawnMu sync.Mutex
		spawns  int
	)
	dispatcher := dialBusClient(t, b, "dispatcher")
	if _, err := dispatcher.Subscribe(ctx, spawnSubj, func(m sextant.Message) {
		var req struct {
			Type   string `json:"$type"`
			Job    string `json:"job,omitempty"`
			Prompt string `json:"prompt,omitempty"`
		}
		if err := json.Unmarshal(m.Frame.Record, &req); err != nil || req.Type != workflow.TypeSpawnRequest {
			return
		}
		spawnMu.Lock()
		spawns++
		spawnMu.Unlock()
		ack := workflow.SpawnAck{Type: workflow.TypeSpawnAck, ID: "agent-" + m.Frame.ID[:8], RequestID: m.Frame.ID, Status: workflow.StatusOK}
		ackBytes, _ := json.Marshal(ack)
		_ = dispatcher.Publish(ctx, spawnSubj, json.RawMessage(ackBytes))
		stepID := parseDirective(req.Prompt, "RUN_STEP")
		ev := workflow.RunEvent{Step: stepID, Status: workflow.StepDone, Outcome: workflow.RunDone}
		name := "brief.idem." + req.Job
		if _, err := dispatcher.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"brief","outcome":"done"}`)); err != nil {
			return
		}
		ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "brief", Version: 1}}
		_ = dispatcher.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal())
	}, sextant.DeliverAll()); err != nil {
		t.Fatalf("subscribe dispatcher: %v", err)
	}

	run := workflow.Run{
		ID: "01IDEMREPLAY00000000000000", Status: workflow.RunRunning, Objective: "run once",
		Steps: []workflow.RunStep{
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	// Coordinator A drives it to done.
	coordA := dialBusClient(t, b, "coord-A")
	stopA := startConsumerAs(t, ctx, coordA, spawnSubj, 10*time.Second)
	first := pollRun(t, ctx, requester, run.ID, 15*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if first.Status != workflow.RunDone {
		t.Fatalf("first run did not reach done: %q", first.Status)
	}
	spawnMu.Lock()
	spawnsAfterFirst := spawns
	spawnMu.Unlock()
	ownerAfterFirst := first.Owner
	revAfterFirst := pollRevision(t, ctx, requester, run.ID)
	stopA()

	// Coordinator B subscribes: DeliverAll replays the same retained start. It must be a
	// no-op (terminal run skipped) — no new dispatch, no owner change, no status change.
	coordB := dialBusClient(t, b, "coord-B")
	startConsumerAs(t, ctx, coordB, spawnSubj, 10*time.Second)

	// Give the replay + (skipped) handling time to run.
	time.Sleep(2 * time.Second)

	spawnMu.Lock()
	spawnsAfterReplay := spawns
	spawnMu.Unlock()
	if spawnsAfterReplay != spawnsAfterFirst {
		t.Errorf("replay of a finished run re-dispatched: spawns %d → %d (want unchanged) — idempotency broken", spawnsAfterFirst, spawnsAfterReplay)
	}
	after := pollRun(t, ctx, requester, run.ID, 2*time.Second, func(r workflow.Run) bool { return true })
	if after.Status != workflow.RunDone {
		t.Errorf("replay changed a terminal run's status: %q (want done)", after.Status)
	}
	if after.Owner != ownerAfterFirst {
		t.Errorf("replay clobbered the owner: %q → %q (want unchanged)", ownerAfterFirst, after.Owner)
	}
	if revAfterReplay := pollRevision(t, ctx, requester, run.ID); revAfterReplay != revAfterFirst {
		t.Errorf("replay wrote the envelope of a terminal run: revision %d → %d (want unchanged)", revAfterFirst, revAfterReplay)
	}
}

// TestAdopt_StaleStartNoEnvelopeSkipped is the TASK-192 stale-start class under the durable
// mechanism: a replayed run.start whose run artifact never existed (a foreign or
// long-deleted start) is skipped QUIETLY — no ack, no crash-loop. The old New-only delivery
// handled this by ignoring all history; the durable mechanism handles it by the shouldAdopt
// not-found skip, so a stale start can never wedge a restarted coordinator.
func TestAdopt_StaleStartNoEnvelopeSkipped(t *testing.T) {
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	requester := dialBusClient(t, b, "requester")
	consumer := dialBusClient(t, b, "consumer")

	// A historical start whose run artifact is never written — published before any consumer.
	if _, err := requester.PublishMsg(ctx, workflow.RunStartSubject,
		workflow.RunStartRecord(workflow.RunStartRequest{ID: "01STALENORUN00000000000000"})); err != nil {
		t.Fatalf("publish stale start: %v", err)
	}

	acks := collectStartAcks(t, ctx, requester)
	startConsumerAs(t, ctx, consumer, "msg.topic.spawn", 2*time.Second)

	// Let DeliverAll replay + the guard skip.
	time.Sleep(1500 * time.Millisecond)
	if got := acks(); len(got) != 0 {
		t.Fatalf("a stale run.start with no envelope was acted on (%d ack(s), want 0) — it should be skipped quietly, not adopted or crash-looped", len(got))
	}
}

// --- helpers ---

// dialBusClientNoClose is dialBusClient without the t.Cleanup Close, for a test that closes
// the client itself mid-test (to model a coordinator process crash → owner offline).
func dialBusClientNoClose(t *testing.T, b *bus.Bus, id string) *sextant.Client {
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
	return c
}

// waitOffline polls the clients directory until id is offline (or fails the test).
func waitOffline(t *testing.T, ctx context.Context, c *sextant.Client, id string) {
	t.Helper()
	end := time.Now().Add(10 * time.Second)
	for time.Now().Before(end) {
		clients, err := c.ListClients(ctx)
		if err == nil {
			online := false
			for _, ci := range clients {
				if ci.ID == id {
					online = ci.Online
				}
			}
			if !online {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("client %s never went offline within 10s", id)
}

// pollRevision returns the run envelope's current revision (0 if unreadable).
func pollRevision(t *testing.T, ctx context.Context, c *sextant.Client, runID string) uint64 {
	t.Helper()
	art, err := c.GetArtifact(ctx, workflow.RunStateName(runID))
	if err != nil {
		return 0
	}
	return art.Revision
}
