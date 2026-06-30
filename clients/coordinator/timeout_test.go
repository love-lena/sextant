package main

// TASK-257 proof: the coordinator honours a step's DECLARED per-step timeout
// (RunStep.TimeoutSecs), falling back to the run-wide --step-timeout only when a step
// declares none. This is AC#2's mechanical part — the per-step/per-workflow timeout is
// configurable FROM THE RUN DEFINITION, not a hardcoded constant — driven through the
// REAL coordinator dispatch path (runDispatch → awaitAck/awaitStepDone), not a mock.
//
// The adversarial property: two runs declaring DIFFERENT step timeouts each get the one
// they declared.
//   - A step declaring a 1s timeout whose worker is slow (reports done after ~3s) BLOCKS
//     the run — and does so in about 1s, NOT the long --step-timeout the coordinator was
//     started with. The short elapsed time is what proves the PER-STEP value fired and
//     not the flag default (a fake pass would block only at the flag's deadline).
//   - A step declaring a 10s timeout whose worker reports done after ~1.2s REACHES done —
//     proving a longer declared timeout is honoured and not clamped to the slow step's.
//
// Both runs share ONE coordinator started with a deliberately LARGE flag default, so any
// reliance on the flag (the pre-fix behaviour, where every step ran at one timeout) makes
// the first run block too late (test times out) or the assertions fail.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/conventions/workflow/go"
	"github.com/love-lena/sextant/sdk/go"
)

// slowWorkDispatcher acks every spawn.request, then reports the step done after doneAfter
// (work steps) — long enough that a tight per-step timeout fires before it. A brief step is
// answered promptly with a real artifact so a run that DOES advance can terminate cleanly.
func slowWorkDispatcher(t *testing.T, ctx context.Context, d *sextant.Client, spawnSubj string, doneAfter time.Duration) {
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
		ack := workflow.SpawnAck{Type: workflow.TypeSpawnAck, ID: "agent-" + m.Frame.ID[:8], RequestID: m.Frame.ID, Status: workflow.StatusOK}
		ackBytes, _ := json.Marshal(ack)
		if err := d.Publish(ctx, spawnSubj, json.RawMessage(ackBytes)); err != nil {
			return
		}
		stepID := parseDirective(req.Prompt, "RUN_STEP")
		ev := workflow.RunEvent{Step: stepID, Status: workflow.StepDone}
		var delay time.Duration
		if strings.Contains(req.Prompt, "stopping brief") {
			// Prompt brief with a real artifact: a run that advances past its work step ends
			// cleanly (no second timeout on the brief).
			ev.Outcome = workflow.RunDone
			name := "brief.stopping." + req.Job
			if _, err := d.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"brief","outcome":"done"}`)); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "stopping", Version: 1}}
		} else {
			// The work step is SLOW — its done lands after doneAfter, so a tight per-step
			// timeout expires first. A real deliverable, so a step that DOES finish in time
			// passes the existence gate rather than blocking for the wrong reason.
			delay = doneAfter
			name := "deliverable." + req.Job + "." + stepID
			if _, err := d.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"work"}`)); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "work", Version: 1}}
		}
		go func() {
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
			}
			_ = d.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal())
		}()
	}, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("slowWorkDispatcher Subscribe: %v", err)
	}
}

// TestRun_HonoursDeclaredStepTimeout drives two runs with DIFFERENT declared per-step
// timeouts through one coordinator started with a LARGE flag default, and asserts each run
// gets the timeout IT declared (TASK-257 AC#2).
func TestRun_HonoursDeclaredStepTimeout(t *testing.T) {
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

	// The worker reports a work step done after 3s. A step declaring a 1s timeout must
	// block before then; a step declaring a 10s timeout must complete.
	slowWorkDispatcher(t, ctx, dispatcher, spawnSubj, 3*time.Second)
	// Flag default is LARGE (30s): if the coordinator ignored the per-step value and used
	// the flag, the 1s run would not block until 30s — far past this test's deadline. So a
	// prompt block proves the PER-STEP timeout, not the flag, governs the step.
	startListenConsumer(t, ctx, consumer, spawnSubj, 30*time.Second)

	// Run A: a 1s declared step timeout. The worker is slow (3s), so the step must time
	// out and the run must BLOCK — within ~1s, not the 30s flag.
	runA := workflow.Run{
		ID: "01TIMEOUTSHORT000000000000", Status: workflow.RunRunning, Objective: "tight per-step timeout",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "slow work", Kind: workflow.KindWork, Status: workflow.StepRunning, TimeoutSecs: 1},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	startA := time.Now()
	writeRunAndStart(t, ctx, requester, runA, "")
	gotA := pollRun(t, ctx, requester, runA.ID, 15*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	elapsedA := time.Since(startA)
	if gotA.Status != workflow.RunBlocked {
		t.Fatalf("run A (1s step timeout, 3s worker) must BLOCK on the timed-out step; got %q, steps=%+v", gotA.Status, gotA.Steps)
	}
	// PROPERTY: the block came from the DECLARED 1s timeout, not the 30s flag. Allow generous
	// slack for scheduling, but it must be well under the flag default (and under the 3s the
	// worker would have taken to finish).
	if elapsedA >= 10*time.Second {
		t.Fatalf("run A blocked after %s — too slow to be the declared 1s per-step timeout; the coordinator appears to use the 30s flag, not the step's TimeoutSecs", elapsedA)
	}

	// Run B: a 10s declared step timeout on the SAME slow (3s) worker. The step finishes
	// within its budget, so the run must reach DONE — proving a longer declared timeout is
	// honoured and a slow-but-in-budget step is not killed.
	runB := workflow.Run{
		ID: "01TIMEOUTLONG0000000000000", Status: workflow.RunRunning, Objective: "roomy per-step timeout",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "slow work", Kind: workflow.KindWork, Status: workflow.StepRunning, TimeoutSecs: 10},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, runB, "")
	gotB := pollRun(t, ctx, requester, runB.ID, 20*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if gotB.Status != workflow.RunDone {
		t.Fatalf("run B (10s step timeout, 3s worker) must reach DONE (the step finishes within its declared budget); got %q, steps=%+v", gotB.Status, gotB.Steps)
	}
}
