package main

// D8 — the verify-step proofs. A VERIFY step (KindVerify) dispatches an INDEPENDENT
// verifier worker that builds + runs tests + checks each AC adversarially against the run's
// real deliverable, and gates the RUN: a clean verification advances (the run can reach the
// brief and done); a verification that reports outcome=blocked (DoD not met) BLOCKS the run,
// so it can NEVER reach done over a failed verification. This fixes D8: the old "self-review"
// step was shallow prose that claimed "ACs met" without building/testing, so the engine
// certified non-building deliverables as done.
//
// RED REPRODUCTION RECIPE (so a QA pass can reproduce the RED):
//   The gate lives in runVerify (clients/coordinator/main.go): after the shared
//   runDispatch, it inspects the worker's run.event outcome and returns workflow.RunBlocked
//   when the verifier reported blocked. To flip TestVerify_BlockedVerificationBlocksRun RED,
//   make runVerify ignore the outcome (treat verify exactly like a work step):
//
//       func (co *coordinator) runVerify(step *workflow.RunStep) (string, error) {
//       -   if err := co.runDispatch(step, co.verifyPrompt(step)); err != nil {
//       -       return "", err
//       -   }
//       -   co.mu.Lock()
//       -   ev := co.doneEvents[step.ID]
//       -   co.mu.Unlock()
//       -   if ev.Outcome == workflow.RunBlocked {
//       -       co.appendActivity("✗", ...)
//       -       return workflow.RunBlocked, nil
//       -   }
//       -   return "", nil
//       +   return "", co.runDispatch(step, co.verifyPrompt(step))
//       }
//
//   With that, the blocked verifier's verdict is ignored, the run advances to the brief and
//   reaches done → TestVerify_BlockedVerificationBlocksRun FAILS (RED). Restore the outcome
//   gate → GREEN.
//   Command: go test ./clients/coordinator/ -run TestVerify -count=1 -race

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/conventions/workflow/go"
	"github.com/love-lena/sextant/sdk/go"
)

// verifyDispatcher subscribes to the spawn subject and serves three kinds of worker spawn,
// keyed off the prompt: a WORK step (produces a real deliverable), a VERIFY step (recognised
// by the embedded VERIFICATION CHARTER — produces a real verdict artifact and reports the
// supplied verifyOutcome), and the BRIEF step (produces a real brief, outcome done). It
// records, per kind, the order steps were dispatched so a test can prove the brief never ran
// after a blocked verification. The verifier is a SEPARATE dispatch from the builder.
func verifyDispatcher(t *testing.T, ctx context.Context, d *sextant.Client, spawnSubj, verifyOutcome string, dispatched *[]string, mu *sync.Mutex) {
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
		switch {
		case strings.Contains(req.Prompt, "VERIFICATION CHARTER"):
			mu.Lock()
			*dispatched = append(*dispatched, "verify:"+stepID)
			mu.Unlock()
			// The independent verifier produces a REAL verdict artifact (existence-gated)
			// and reports the test-supplied outcome (done = DoD met; blocked = DoD not met).
			name := "verdict." + req.Job + "." + stepID
			if _, err := d.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"verdict","outcome":"`+verifyOutcome+`"}`)); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "verdict", Version: 1}}
			ev.Outcome = verifyOutcome
		case strings.Contains(req.Prompt, "stopping brief"):
			mu.Lock()
			*dispatched = append(*dispatched, "brief:"+stepID)
			mu.Unlock()
			name := "brief.stopping." + req.Job
			if _, err := d.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"brief","outcome":"done"}`)); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "brief", Version: 1}}
			ev.Outcome = workflow.RunDone
		default: // a WORK step
			mu.Lock()
			*dispatched = append(*dispatched, "work:"+stepID)
			mu.Unlock()
			name := "deliverable." + req.Job + "." + stepID
			if _, err := d.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"work","step":"`+stepID+`"}`)); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "work", Version: 1}}
		}
		_ = d.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal())
	}, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("verifyDispatcher Subscribe: %v", err)
	}
}

// TestVerify_CleanVerificationAdvances: a work → verify → brief run where the verifier
// reports outcome=done (DoD met, with a real verdict artifact) advances past the verify
// step to the brief and reaches done. Proves a verify step that passes does not block.
func TestVerify_CleanVerificationAdvances(t *testing.T) {
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

	var dispatched []string
	var mu sync.Mutex
	verifyDispatcher(t, ctx, dispatcher, spawnSubj, workflow.RunDone, &dispatched, &mu)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	runID := "01VERIFYCLEAN00000000000AB"
	writeRunAndStart(t, ctx, requester, workflow.Run{
		ID: runID, Status: workflow.RunRunning, Objective: "build and verify the thing",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "build it", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "verify", Label: "verify the deliverable", Kind: workflow.KindVerify, Status: workflow.StepUpcoming},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}, "")

	got := pollRun(t, ctx, requester, runID, 25*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Fatalf("a clean verification must let the run reach done; got %q. steps=%+v", got.Status, got.Steps)
	}
	if s := stepStatus(got, "verify"); s != workflow.StepDone {
		t.Errorf("verify step status = %q, want done", s)
	}
	// The brief must have run AFTER the verify step (the ordering: verify gates the brief).
	mu.Lock()
	defer mu.Unlock()
	if !contains(dispatched, "verify:verify") || !contains(dispatched, "brief:brief") {
		t.Fatalf("expected both a verify and a brief dispatch; got %v", dispatched)
	}
	if indexOf(dispatched, "brief:brief") < indexOf(dispatched, "verify:verify") {
		t.Errorf("brief ran before verify; verify must gate the brief. order=%v", dispatched)
	}
}

// TestVerify_BlockedVerificationBlocksRun is THE D8 load-bearing test: the independent
// verifier reports outcome=blocked (DoD NOT met — e.g. the build failed or an AC is unmet),
// attaching a real verdict artifact. The run must BLOCK and NEVER reach the brief or done —
// a failed verification cannot be rubber-stamped into a done run. RED if runVerify ignores
// the verifier's outcome (see the recipe at the top of this file), GREEN with the gate.
func TestVerify_BlockedVerificationBlocksRun(t *testing.T) {
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

	var dispatched []string
	var mu sync.Mutex
	verifyDispatcher(t, ctx, dispatcher, spawnSubj, workflow.RunBlocked, &dispatched, &mu)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	runID := "01VERIFYBLOCK00000000000AB"
	writeRunAndStart(t, ctx, requester, workflow.Run{
		ID: runID, Status: workflow.RunRunning, Objective: "build and verify the thing",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "build it", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "verify", Label: "verify the deliverable", Kind: workflow.KindVerify, Status: workflow.StepUpcoming},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}, "")

	got := pollRun(t, ctx, requester, runID, 25*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status == workflow.RunDone {
		t.Fatalf("a BLOCKED verification reached done — a failed verification was rubber-stamped (D8 violated). steps=%+v", got.Steps)
	}
	if got.Status != workflow.RunBlocked {
		t.Errorf("a blocked verification must block the run; got status %q", got.Status)
	}
	// The brief step must NEVER have run — the run blocked at verify, before certifying done.
	mu.Lock()
	defer mu.Unlock()
	if contains(dispatched, "brief:brief") {
		t.Fatalf("the brief step ran AFTER a failed verification — verify must gate the brief (D8). order=%v", dispatched)
	}
	if !contains(dispatched, "verify:verify") {
		t.Fatalf("the verify step never ran; order=%v", dispatched)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func indexOf(ss []string, s string) int {
	for i, x := range ss {
		if x == s {
			return i
		}
	}
	return -1
}
