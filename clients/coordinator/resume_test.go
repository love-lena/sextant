package main

// Run resume/retry integration tests (TASK-267) over a REAL bus.
//
// The live failure these guard (D16, found in a hermetic e2e): when the host lost
// network mid-run, a dispatched worker's model calls failed; the worker drained and
// published a step-done run.event with artifacts:0; the coordinator's proof gate
// (correctly, for a hollow step) blocked the run. But `blocked` was then TERMINAL — a
// re-published run.start was skipped as "already blocked (idempotent replay)" — so a
// TRANSIENT network interruption PERMANENTLY blocked the run, with no resume or retry.
// That fails DoD: "it's okay that a run hangs when the machine loses network, but the
// run must RESUME or be RETRIABLE when the connection is re-established."
//
// The fix distinguishes terminal-FINAL (done/cancelled — never re-run) from `blocked`
// (resumable). These tests drive the REAL consumer path (newStartConsumer → handle →
// shouldAdopt → claimOwnership → adopt → walk) and assert the PROPERTY, not one path:
//
//   - TestResume_BlockedRunResumesOnReissue: a run is driven to `blocked` by a hollow
//     work step (the D16 interrupted case — step-done with artifacts:0). The test FIRST
//     confirms the run actually reached `blocked` (the fake-pass guard — it does not
//     skip straight to the happy path). Then a now-cooperating dispatcher is enabled and
//     the run.start is re-published: the coordinator RE-ADOPTS the run (it is NOT skipped
//     as already-blocked), the previously-failed step goes `running` again (re-dispatched
//     fresh), and the run reaches `done`. A done run is never re-run on a further re-issue.
//   - TestResume_DoneRunStillSkipsOnReissue: the done-skip half of the distinction — a
//     re-issued run.start for an already-`done` run is a no-op (no re-dispatch, no owner
//     clobber, no status change), so resume never re-runs a completed run.

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/conventions/workflow/go"
	"github.com/love-lena/sextant/sdk/go"
)

// toggleableDispatcher cooperates on every spawn.request, but its WORK-step behaviour is
// switched by cooperating: while false it is HOLLOW (reports the work step done with NO
// artifact — the D16 interrupted-worker case that blocks the run); once flipped true it
// produces a real deliverable so a re-dispatched work step succeeds. The brief step is
// always cooperative (a real brief artifact), isolating the resume on the work step.
// spawns counts every spawn.request so a re-dispatch is observable; workSpawns counts only
// work-step dispatches so a re-dispatch of the failed step is distinguishable from the
// brief.
func toggleableDispatcher(t *testing.T, ctx context.Context, d *sextant.Client, spawnSubj string, cooperating *atomic.Bool, spawns, workSpawns *int64) {
	t.Helper()
	_, err := d.Subscribe(ctx, spawnSubj, func(m sextant.Message) {
		var req struct {
			Type   string `json:"$type"`
			Job    string `json:"job,omitempty"`
			Prompt string `json:"prompt,omitempty"`
		}
		if err := json.Unmarshal(m.Frame.Record, &req); err != nil || req.Type != workflow.TypeSpawnRequest {
			return
		}
		atomic.AddInt64(spawns, 1)
		ack := workflow.SpawnAck{Type: workflow.TypeSpawnAck, ID: "agent-" + m.Frame.ID[:8], RequestID: m.Frame.ID, Status: workflow.StatusOK}
		ackBytes, _ := json.Marshal(ack)
		if err := d.Publish(ctx, spawnSubj, json.RawMessage(ackBytes)); err != nil {
			return
		}
		stepID := parseDirective(req.Prompt, "RUN_STEP")
		ev := workflow.RunEvent{Step: stepID, Status: workflow.StepDone}
		switch {
		case strings.Contains(req.Prompt, "stopping brief"):
			ev.Outcome = workflow.RunDone
			name := "brief.stopping." + req.Job
			if !putArtifact(ctx, d, name, json.RawMessage(`{"$type":"brief","outcome":"done"}`)) {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "stopping", Version: 1}}
		default:
			// A WORK step. Count it so a re-dispatch of the failed step is visible.
			atomic.AddInt64(workSpawns, 1)
			if cooperating.Load() {
				// Connection restored: produce the real deliverable so the re-dispatched
				// step succeeds and the run advances.
				name := "deliverable." + req.Job + "." + stepID
				if !putArtifact(ctx, d, name, json.RawMessage(`{"$type":"work","step":"`+stepID+`"}`)) {
					return
				}
				ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "work", Version: 1}}
			}
			// else: HOLLOW — report done with NO artifact (the D16 drained-worker case).
			// The coordinator's count gate blocks the run.
		}
		_ = d.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal())
	}, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("toggleableDispatcher Subscribe: %v", err)
	}
}

// TestResume_BlockedRunResumesOnReissue is the TASK-267 core property: a run blocked by a
// transient interruption (a hollow work step) RESUMES when re-issued — the failed step is
// re-dispatched fresh and the run reaches done. Re-publishing run.start is the resume
// trigger (no new control verb — the existing surface the dash/operator already uses).
func TestResume_BlockedRunResumesOnReissue(t *testing.T) {
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	consumer := dialBusClient(t, b, "consumer")
	requester := dialBusClient(t, b, "requester")
	dispatcher := dialBusClient(t, b, "dispatcher")
	spawnSubj := "msg.topic.spawn"

	var (
		cooperating atomic.Bool // false → hollow work step (blocks); true → real deliverable
		spawns      int64
		workSpawns  int64
	)
	toggleableDispatcher(t, ctx, dispatcher, spawnSubj, &cooperating, &spawns, &workSpawns)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	run := workflow.Run{
		ID: "01RESUMEBLOCKED00000000000", Status: workflow.RunRunning, Objective: "produce, get interrupted, then resume",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "do work", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	// FAKE-PASS GUARD #1: the run must ACTUALLY reach `blocked` first (the interrupted
	// state). A test that skipped straight to the happy path would prove nothing.
	blocked := pollRun(t, ctx, requester, run.ID, 15*time.Second, func(r workflow.Run) bool {
		return r.Status == workflow.RunBlocked
	})
	if blocked.Status != workflow.RunBlocked {
		t.Fatalf("setup: run did not reach blocked (the interrupted case); status=%q steps=%+v", blocked.Status, blocked.Steps)
	}
	// The work step is the one that failed (left waiting next to the blocked run); the
	// brief must NOT have run (the run blocked before it).
	if s := stepStatus(blocked, "s1"); s == workflow.StepDone {
		t.Fatalf("setup: work step s1 unexpectedly done; the hollow case should have failed it (status=%q)", s)
	}
	if s := stepStatus(blocked, "brief"); s == workflow.StepDone {
		t.Fatalf("setup: brief step ran before the work step blocked the run (status=%q)", s)
	}
	spawnsAtBlock := atomic.LoadInt64(&spawns)
	workSpawnsAtBlock := atomic.LoadInt64(&workSpawns)
	if workSpawnsAtBlock == 0 {
		t.Fatalf("setup: the work step was never dispatched (workSpawns=0)")
	}

	// CONNECTION RESTORED: the dispatcher now produces the real deliverable.
	cooperating.Store(true)

	// RESUME: re-publish run.start for the same run id. The coordinator must RE-ADOPT the
	// blocked run (not skip it as "already blocked"), reset the run to running, re-dispatch
	// the failed work step FRESH, and drive the run to done.
	if _, err := requester.PublishMsg(ctx, workflow.RunStartSubject,
		workflow.RunStartRecord(workflow.RunStartRequest{ID: run.ID})); err != nil {
		t.Fatalf("re-publish run.start (resume): %v", err)
	}

	// Property: the previously-failed work step is RE-DISPATCHED (it goes running again,
	// observable as a new work-step spawn) and the run reaches done.
	done := pollRun(t, ctx, requester, run.ID, 20*time.Second, func(r workflow.Run) bool {
		return r.Status == workflow.RunDone
	})
	if done.Status != workflow.RunDone {
		t.Fatalf("a blocked run was not resumed on re-issue: status=%q steps=%+v — a transient interruption permanently blocked it", done.Status, done.Steps)
	}
	if s := stepStatus(done, "s1"); s != workflow.StepDone {
		t.Fatalf("the failed work step was not re-dispatched to completion on resume: s1 status=%q (want done)", s)
	}
	if s := stepStatus(done, "brief"); s != workflow.StepDone {
		t.Errorf("brief step did not complete after resume: status=%q", s)
	}
	if done.Owner == "" {
		t.Errorf("resumed run has no owner; re-adoption must (re)set the owner")
	}
	// The resume re-dispatched the failed work step: more work-step spawns than at block.
	if w := atomic.LoadInt64(&workSpawns); w <= workSpawnsAtBlock {
		t.Errorf("the failed work step was not re-dispatched on resume: workSpawns %d → %d (want an increase)", workSpawnsAtBlock, w)
	}
	if sp := atomic.LoadInt64(&spawns); sp <= spawnsAtBlock {
		t.Errorf("resume dispatched nothing: spawns %d → %d (want an increase)", spawnsAtBlock, sp)
	}

	// FAKE-PASS GUARD #2: a now-DONE run is never re-run on a further re-issue (resume must
	// not re-run a completed run). Re-publish once more; it must be a no-op.
	spawnsAfterResume := atomic.LoadInt64(&spawns)
	ownerAfterResume := done.Owner
	revAfterResume := pollRevision(t, ctx, requester, run.ID)
	if _, err := requester.PublishMsg(ctx, workflow.RunStartSubject,
		workflow.RunStartRecord(workflow.RunStartRequest{ID: run.ID})); err != nil {
		t.Fatalf("re-publish run.start (done-skip check): %v", err)
	}
	time.Sleep(2 * time.Second)
	if sp := atomic.LoadInt64(&spawns); sp != spawnsAfterResume {
		t.Errorf("re-issuing a DONE run re-dispatched: spawns %d → %d (want unchanged) — resume must never re-run a completed run", spawnsAfterResume, sp)
	}
	after := pollRun(t, ctx, requester, run.ID, 2*time.Second, func(r workflow.Run) bool { return true })
	if after.Status != workflow.RunDone {
		t.Errorf("re-issuing a done run changed its status: %q (want done)", after.Status)
	}
	if after.Owner != ownerAfterResume {
		t.Errorf("re-issuing a done run clobbered the owner: %q → %q", ownerAfterResume, after.Owner)
	}
	if rev := pollRevision(t, ctx, requester, run.ID); rev != revAfterResume {
		t.Errorf("re-issuing a done run wrote the envelope: revision %d → %d (want unchanged)", revAfterResume, rev)
	}
}

// TestResume_DoneRunStillSkipsOnReissue is the terminal-FINAL half of the TASK-267
// distinction: a re-issued run.start for an already-`done` run is a no-op — the
// idempotent-replay guard (TASK-259) is intact for done runs. This is distinct from the
// blocked-resume path: resume re-runs only a `blocked` run, never a completed one.
func TestResume_DoneRunStillSkipsOnReissue(t *testing.T) {
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

	var spawns int64
	var dummyWork int64
	var cooperating atomic.Bool
	cooperating.Store(true) // always produce real deliverables → the run reaches done
	toggleableDispatcher(t, ctx, dispatcher, spawnSubj, &cooperating, &spawns, &dummyWork)

	run := workflow.Run{
		ID: "01RESUMEDONESKIP0000000000", Status: workflow.RunRunning, Objective: "complete normally",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "do work", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	consumer := dialBusClient(t, b, "consumer")
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	first := pollRun(t, ctx, requester, run.ID, 15*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if first.Status != workflow.RunDone {
		t.Fatalf("run did not reach done: %q", first.Status)
	}
	spawnsAtDone := atomic.LoadInt64(&spawns)
	ownerAtDone := first.Owner
	revAtDone := pollRevision(t, ctx, requester, run.ID)

	// Re-issue the run.start for the already-done run. It must be skipped (idempotent
	// replay) — no re-dispatch, no owner clobber, no status/revision change.
	if _, err := requester.PublishMsg(ctx, workflow.RunStartSubject,
		workflow.RunStartRecord(workflow.RunStartRequest{ID: run.ID})); err != nil {
		t.Fatalf("re-publish run.start (done): %v", err)
	}
	time.Sleep(2 * time.Second)

	if sp := atomic.LoadInt64(&spawns); sp != spawnsAtDone {
		t.Errorf("re-issuing a done run re-dispatched: spawns %d → %d (want unchanged)", spawnsAtDone, sp)
	}
	after := pollRun(t, ctx, requester, run.ID, 2*time.Second, func(r workflow.Run) bool { return true })
	if after.Status != workflow.RunDone {
		t.Errorf("re-issuing a done run changed its status: %q (want done)", after.Status)
	}
	if after.Owner != ownerAtDone {
		t.Errorf("re-issuing a done run clobbered the owner: %q → %q", ownerAtDone, after.Owner)
	}
	if rev := pollRevision(t, ctx, requester, run.ID); rev != revAtDone {
		t.Errorf("re-issuing a done run wrote the envelope: revision %d → %d (want unchanged)", revAtDone, rev)
	}
}
