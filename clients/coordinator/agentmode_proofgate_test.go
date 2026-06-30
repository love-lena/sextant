package main

// TASK-242 AC#7 — THE load-bearing proof-gate-floor test. Agent-mode decisions sit ON the
// TASK-243 deterministic existence gate and can NEVER bypass it: an advance/done decision
// over a step whose reported artifact does NOT exist on the bus must NOT reach run done —
// the deterministic gate (verifyReportedArtifactsExist) rejects it FIRST, before the agent
// is ever consulted.
//
// RED REPRODUCTION RECIPE (so the DoD stickler can reproduce the RED themselves):
//   The gate runs inside runDispatch (work step) / runBrief (brief step), BEFORE walk()
//   calls reviewAndApply. To flip this test RED, BYPASS the gate in agent mode — e.g. in
//   clients/coordinator/main.go runDispatch, guard the existence check so it is skipped
//   when the run is in agent mode:
//
//       -   if err := co.verifyReportedArtifactsExist(ev.Artifacts); err != nil {
//       -       return err
//       -   }
//       +   if !co.agentEnabled() { // BYPASS: trust the agent in agent mode
//       +       if err := co.verifyReportedArtifactsExist(ev.Artifacts); err != nil {
//       +           return err
//       +       }
//       +   }
//
//   With that bypass the rubber-stamp agent's advance lets the run reach done over the
//   phantom → this test FAILS (RED). Restore the unconditional gate → GREEN.
//   Command: go test ./clients/coordinator/ -run TestAgentMode_AdvanceOverPhantomStillBlocks -count=1 -race

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

// TestAgentMode_AdvanceOverPhantomStillBlocks: the work step reports producing an artifact
// that is NEVER created on the bus (a phantom — the 01KW8J2N fabrication class), and the
// coordinator agent rubber-stamps ADVANCE. The deterministic existence gate must reject the
// phantom and BLOCK the run; the agent's advance can NEVER certify done over a deliverable
// that does not exist. This is RED if the gate is bypassed in agent mode (see recipe above),
// GREEN with it.
func TestAgentMode_AdvanceOverPhantomStillBlocks(t *testing.T) {
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

	// A worker that, on the work step, reports producing phantom-<job> WITHOUT creating it
	// (passes the count gate, must fail the existence gate). The brief step (if reached)
	// produces a real brief — so the ONLY thing that can block this run is the work-step
	// existence gate, isolating AC#7.
	worker := func(t *testing.T, ctx context.Context, d *sextant.Client, job, stepID, prompt string) []workflow.ProducedArtifact {
		if strings.Contains(prompt, "stopping brief") {
			name := "brief.real." + job
			if _, err := d.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"brief","outcome":"done"}`)); err != nil {
				return nil
			}
			return []workflow.ProducedArtifact{{Name: name, Kind: "brief", Version: 1}}
		}
		// WORK step: report a produced artifact that is NEVER created on the bus.
		return []workflow.ProducedArtifact{{Name: "phantom-" + job, Kind: "poem", Version: 1}}
	}
	// The agent RUBBER-STAMPS advance — the adversarial case. If the gate were bypassed, this
	// advance would let the run reach done over the phantom.
	agentModeHarness(t, ctx, b, dispatcher, spawnSubj, worker, alwaysAdvance, nil)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	runID := "01AGENTPHANTOM000000000000"
	startAgentRun(t, ctx, requester, runID, "write a poem", []workflow.RunStep{
		{ID: "s1", Label: "write the poem", Kind: workflow.KindWork, Status: workflow.StepRunning},
		{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
	})

	got := pollRun(t, ctx, requester, runID, 20*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status == workflow.RunDone {
		t.Fatalf("agent advance reached done over a NON-EXISTENT artifact — the deterministic existence gate was bypassed in agent mode (AC#7 violated). steps=%+v", got.Steps)
	}
	if got.Status != workflow.RunBlocked {
		t.Errorf("an advance over a phantom artifact must BLOCK the run; got status %q", got.Status)
	}
}

// TestAgentMode_ReviewerFlagsProseOnlyClaim is the AC#5 proof (the content-truthfulness
// gap TASK-243 option A routes here): a brief step PRODUCES a real brief artifact but the
// brief's PROSE claims a deliverable that has no corresponding typed produced ref. The
// deterministic gate CANNOT catch this (the brief artifact exists; there is no phantom typed
// ref to existence-check), so the run would reach done deterministically. The reviewer AGENT,
// reading the produced refs (and in production the brief CONTENT), recognises the claimed
// deliverable is absent from the typed refs and returns redo-with-feedback — NOT advance.
// This proves the reviewer's content read is the layer that closes the residual gap, and the
// review seam carries enough (the produced refs) for the agent to make that call.
func TestAgentMode_ReviewerFlagsProseOnlyClaim(t *testing.T) {
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

	// The brief step produces a REAL brief artifact (so the deterministic gate passes) whose
	// prose CLAIMS a "poem" deliverable — but the brief reports only ONE typed ref (the brief
	// itself), no typed ref for the claimed poem. On a redo, the worker behaves the same;
	// this test asserts the reviewer's FIRST verdict, so a single brief attempt is enough.
	var briefSeen int
	var bmu sync.Mutex
	worker := func(t *testing.T, ctx context.Context, d *sextant.Client, job, stepID, prompt string) []workflow.ProducedArtifact {
		if strings.Contains(prompt, "stopping brief") {
			bmu.Lock()
			briefSeen++
			bmu.Unlock()
			name := "brief.proseclaim." + job
			// Prose claims a poem that has NO typed produced ref → deterministic gate can't see it.
			// Upsert: the brief is re-dispatched on a redo (same name re-produced).
			if !putArtifact(ctx, d, name, json.RawMessage(`{"$type":"brief","outcome":"done","narrative":"I wrote the poem artifact poem-x"}`)) {
				return nil
			}
			return []workflow.ProducedArtifact{{Name: name, Kind: "brief", Version: 1}}
		}
		name := "deliverable." + job + "." + stepID
		if !putArtifact(ctx, d, name, json.RawMessage(`{"$type":"work"}`)) {
			return nil
		}
		return []workflow.ProducedArtifact{{Name: name, Kind: "work", Version: 1}}
	}
	// A content-aware reviewer: it reads the produced refs (and in production the brief body).
	// For the brief step, the brief's prose claims a poem deliverable, but the typed refs hold
	// only the brief itself — so the reviewer returns redo-with-feedback the FIRST time, then
	// advances (modelling the worker correcting it). A rubber-stamp (always advance) would NOT
	// loop — the fake-pass guard.
	var rmu sync.Mutex
	briefReviews := 0
	decide := func(r workflow.RunReview) workflow.RunDecision {
		if r.Step != "brief" {
			return workflow.RunDecision{Verb: workflow.DecisionAdvance}
		}
		rmu.Lock()
		briefReviews++
		first := briefReviews == 1
		rmu.Unlock()
		// The claimed "poem" deliverable is absent from the typed produced refs → flag it.
		hasPoem := false
		for _, p := range r.Produced {
			if strings.Contains(p.Name, "poem") || p.Kind == "poem" {
				hasPoem = true
			}
		}
		if first && !hasPoem {
			return workflow.RunDecision{Verb: workflow.DecisionRedo, Feedback: "your brief claims a poem deliverable but produced no such artifact — produce it", Reason: "prose claim has no typed deliverable"}
		}
		return workflow.RunDecision{Verb: workflow.DecisionAdvance}
	}
	agentModeHarness(t, ctx, b, dispatcher, spawnSubj, worker, decide, nil)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	runID := "01AGENTPROSECLAIM000000000"
	startAgentRun(t, ctx, requester, runID, "write a poem", []workflow.RunStep{
		{ID: "s1", Label: "draft", Kind: workflow.KindWork, Status: workflow.StepRunning},
		{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
	})

	got := pollRun(t, ctx, requester, runID, 25*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	// The reviewer must have sent the brief back at least once (it did not rubber-stamp the
	// prose-only claim) — proving the content-aware reviewer caught what the gate could not.
	rmu.Lock()
	defer rmu.Unlock()
	if briefReviews < 2 {
		t.Fatalf("the reviewer did not loop on the prose-only claim (briefReviews=%d); a content-aware reviewer must flag it with redo, not rubber-stamp advance", briefReviews)
	}
	bmu.Lock()
	defer bmu.Unlock()
	if briefSeen < 2 {
		t.Errorf("the brief step was not re-dispatched after the reviewer's redo (briefSeen=%d)", briefSeen)
	}
	_ = got
}

// TestAgentMode_NoArtifactCannotReachDone is the AC#7 companion: the worker attaches NO
// artifact (the hollow case), and the agent rubber-stamps advance. The deterministic
// count gate must block the run BEFORE the agent is consulted — an agent advance over an
// empty deliverable can never reach done. (The shell never even reviews the step, because
// the hollow gate fails inside runDispatch first.)
func TestAgentMode_NoArtifactCannotReachDone(t *testing.T) {
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

	// Hollow ONLY on the work step (reports done with NO artifacts); real brief otherwise.
	worker := func(t *testing.T, ctx context.Context, d *sextant.Client, job, stepID, prompt string) []workflow.ProducedArtifact {
		if strings.Contains(prompt, "stopping brief") {
			name := "brief.real." + job
			if _, err := d.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"brief","outcome":"done"}`)); err != nil {
				return nil
			}
			return []workflow.ProducedArtifact{{Name: name, Kind: "brief", Version: 1}}
		}
		return nil // hollow work step
	}
	agentModeHarness(t, ctx, b, dispatcher, spawnSubj, worker, alwaysAdvance, nil)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	runID := "01AGENTHOLLOW00000000000AB"
	startAgentRun(t, ctx, requester, runID, "produce something", []workflow.RunStep{
		{ID: "s1", Label: "do work", Kind: workflow.KindWork, Status: workflow.StepRunning},
		{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
	})

	got := pollRun(t, ctx, requester, runID, 20*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status == workflow.RunDone {
		t.Fatalf("agent advance reached done over a HOLLOW work step (no artifact); the count gate was bypassed in agent mode. steps=%+v", got.Steps)
	}
	if got.Status != workflow.RunBlocked {
		t.Errorf("an advance over a hollow work step must BLOCK the run; got status %q", got.Status)
	}
}
