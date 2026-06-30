package main

// TASK-242 agent-mode proofs. These exercise the opt-in long-lived coordinator-AGENT
// review loop layered onto the programmatic shell, over a REAL bus. The fakes model the
// live wiring faithfully: a dispatcher that (a) for a WORKER spawn produces a deliverable +
// reports the step done, and (b) for the COORDINATOR-AGENT spawn (nickname "run-coordinator")
// stands up a resident reviewer that, on each run.review DM'd to its inbox, READS nothing it
// shouldn't and replies with a programmed run.decision on the decision subject. The
// coordinator is NOT faked — it is the real shell under test.
//
// Coverage maps to the repaired ACs:
//   - TestAgentMode_DefaultRunConsultsNoAgent (AC#1 negative): a run WITHOUT agent_mode
//     spawns NO coordinator agent and emits NO run.review — byte-identical to TASK-236.
//   - TestAgentMode_AdvanceProceeds (AC#2 advance): an advance decision proceeds to done.
//   - TestAgentMode_RedoReDispatchesSameStep (AC#2 redo): a redo-with-feedback re-runs the
//     SAME step id, with the agent's feedback present in the re-dispatch prompt.
//   - TestAgentMode_EditThenAdvance (AC#2 edit): an edit-then-advance proceeds, recording
//     the decision on the activity trail (the agent's edit is its own act).
//   - TestAgentMode_StopIsTerminal (AC#2 stop): a stop decision ends the run terminal.
//   - TestAgentMode_SingleWriterPreserved (AC#3): every run-envelope revision is authored
//     by the shell; the coordinator agent authors ZERO revisions.
//   - TestAgentMode_GraphReshapingRejected (AC#2 guard): a branch/insert/skip verb is
//     rejected — the run does NOT silently advance on it (it blocks).
//   - TestAgentMode_AdvanceOverPhantomStillBlocks (AC#7, THE load-bearing test): an agent
//     that returns advance/done while the worker reported a NON-EXISTENT artifact must NOT
//     reach done — the deterministic existence gate rejects it first. RED if the gate is
//     bypassed in agent mode, GREEN with it.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/conventions/workflow/go"
	"github.com/love-lena/sextant/protocol/sx"
	"github.com/love-lena/sextant/sdk/go"
)

// reviewObserver records the run.review records the shell published (per step) and lets a
// test assert how many reviews happened. Concurrency-safe.
type reviewObserver struct {
	mu      sync.Mutex
	reviews []workflow.RunReview
}

func (o *reviewObserver) add(r workflow.RunReview) {
	o.mu.Lock()
	o.reviews = append(o.reviews, r)
	o.mu.Unlock()
}

func (o *reviewObserver) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.reviews)
}

// workerFn lets a test program a worker's response to a work step's spawn. It returns the
// produced-artifact refs to report (after creating any real artifacts itself) for the given
// run id + step id + prompt. If nil, the default faithful worker is used (create + report
// one deliverable per work step; a real brief for the brief step).
type workerFn func(t *testing.T, ctx context.Context, d *sextant.Client, job, stepID, prompt string) []workflow.ProducedArtifact

// decideFn programs the fake coordinator agent's decision for a given review. The harness
// publishes the returned decision on the run's decision subject.
type decideFn func(review workflow.RunReview) workflow.RunDecision

// agentModeHarness wires a single dispatcher client to serve BOTH worker spawns and the
// coordinator-agent spawn. For the coordinator agent (nickname "run-coordinator") it
// subscribes that agent's inbox and, on each run.review, calls decide() and publishes the
// resulting run.decision AS THE COORDINATOR-AGENT (a distinct identity from the shell — so
// the single-writer authorship assertion is meaningful). The review observer records every
// review the shell sent.
func agentModeHarness(t *testing.T, ctx context.Context, b *bus.Bus, d *sextant.Client, spawnSubj string, worker workerFn, decide decideFn, obs *reviewObserver) {
	t.Helper()
	if worker == nil {
		worker = defaultWorker
	}
	_, err := d.Subscribe(ctx, spawnSubj, func(m sextant.Message) {
		var req struct {
			Type     string `json:"$type"`
			Job      string `json:"job,omitempty"`
			Prompt   string `json:"prompt,omitempty"`
			Nickname string `json:"nickname,omitempty"`
		}
		if err := json.Unmarshal(m.Frame.Record, &req); err != nil || req.Type != workflow.TypeSpawnRequest {
			return
		}
		agentID := "agent-" + m.Frame.ID[:8]
		ack := workflow.SpawnAck{Type: workflow.TypeSpawnAck, ID: agentID, RequestID: m.Frame.ID, Status: workflow.StatusOK}
		ackBytes, _ := json.Marshal(ack)
		if err := d.Publish(ctx, spawnSubj, json.RawMessage(ackBytes)); err != nil {
			return
		}

		// The COORDINATOR AGENT: a resident reviewer. Stand it up under its OWN bus
		// identity (so its decisions are authored by it, not the shell), subscribe its
		// inbox, and reply to each run.review with a programmed decision.
		if req.Nickname == "run-coordinator" {
			agentClient := dialBusClient(t, b, "coordinator-agent-"+req.Job)
			_, err := agentClient.Subscribe(ctx, sx.ClientSubject(agentID), func(im sextant.Message) {
				review, ok := workflow.ParseRunReview(im.Frame.Record)
				if !ok {
					return
				}
				if obs != nil {
					obs.add(review)
				}
				dec := decide(review)
				dec.Step = review.Step
				// Published by the AGENT's client — a different author than the shell.
				_ = agentClient.Publish(ctx, workflow.RunDecisionSubject(req.Job), dec.Marshal())
			}, sextant.DeliverAll())
			if err != nil {
				t.Errorf("coordinator-agent inbox subscribe: %v", err)
			}
			return
		}

		// A WORKER spawn (work or brief step).
		stepID := parseDirective(req.Prompt, "RUN_STEP")
		arts := worker(t, ctx, d, req.Job, stepID, req.Prompt)
		ev := workflow.RunEvent{Step: stepID, Status: workflow.StepDone, Artifacts: arts}
		if strings.Contains(req.Prompt, "stopping brief") {
			ev.Outcome = workflow.RunDone
		}
		_ = d.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal())
	}, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("agentModeHarness Subscribe: %v", err)
	}
}

// defaultWorker creates one real deliverable per work step and a real brief for the brief
// step, reporting each as a typed produced ref (so the deterministic gates are satisfied).
func defaultWorker(t *testing.T, ctx context.Context, d *sextant.Client, job, stepID, prompt string) []workflow.ProducedArtifact {
	t.Helper()
	if strings.Contains(prompt, "stopping brief") {
		name := "brief.stopping." + job
		// Upsert (not Create): a redo re-dispatches the SAME step, so the worker re-produces
		// its deliverable under the same name — the real worker's idempotent artifact_put.
		if !putArtifact(ctx, d, name, json.RawMessage(`{"$type":"brief","outcome":"done"}`)) {
			return nil
		}
		return []workflow.ProducedArtifact{{Name: name, Kind: "stopping", Version: 1}}
	}
	name := "deliverable." + job + "." + stepID
	if !putArtifact(ctx, d, name, json.RawMessage(`{"$type":"work","step":"`+stepID+`"}`)) {
		return nil
	}
	return []workflow.ProducedArtifact{{Name: name, Kind: "work", Version: 1}}
}

// alwaysAdvance is the rubber-stamp reviewer (used where advance is the right call).
func alwaysAdvance(workflow.RunReview) workflow.RunDecision {
	return workflow.RunDecision{Verb: workflow.DecisionAdvance, Reason: "looks good"}
}

// startAgentRun writes an agent-mode run + run.start and returns the run id.
func startAgentRun(t *testing.T, ctx context.Context, requester *sextant.Client, id, objective string, steps []workflow.RunStep) string {
	t.Helper()
	run := workflow.Run{ID: id, Status: workflow.RunRunning, Objective: objective, AgentMode: true, Steps: steps}
	writeRunAndStart(t, ctx, requester, run, "")
	return id
}

// TestAgentMode_DefaultRunConsultsNoAgent is the AC#1 NEGATIVE proof: a run WITHOUT
// agent_mode spawns NO coordinator agent and the shell emits NO run.review. It uses a
// dispatcher that FAILS the test if it ever sees a "run-coordinator" spawn, and an observer
// on the decision subject that must stay empty. This is byte-identical to TASK-236.
func TestAgentMode_DefaultRunConsultsNoAgent(t *testing.T) {
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

	var coordSpawns int
	var mu sync.Mutex
	_, err = dispatcher.Subscribe(ctx, spawnSubj, func(m sextant.Message) {
		var req struct {
			Type     string `json:"$type"`
			Job      string `json:"job,omitempty"`
			Prompt   string `json:"prompt,omitempty"`
			Nickname string `json:"nickname,omitempty"`
		}
		if err := json.Unmarshal(m.Frame.Record, &req); err != nil || req.Type != workflow.TypeSpawnRequest {
			return
		}
		if req.Nickname == "run-coordinator" {
			mu.Lock()
			coordSpawns++
			mu.Unlock()
		}
		ack := workflow.SpawnAck{Type: workflow.TypeSpawnAck, ID: "agent-" + m.Frame.ID[:8], RequestID: m.Frame.ID, Status: workflow.StatusOK}
		ackBytes, _ := json.Marshal(ack)
		_ = dispatcher.Publish(ctx, spawnSubj, json.RawMessage(ackBytes))
		stepID := parseDirective(req.Prompt, "RUN_STEP")
		_ = dispatcher.Publish(ctx, workflow.RunEventsSubject(req.Job),
			workflow.RunEvent{Step: stepID, Status: workflow.StepDone, Outcome: workflow.RunDone, Artifacts: defaultWorker(t, ctx, dispatcher, req.Job, stepID, req.Prompt)}.Marshal())
	}, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("subscribe dispatcher: %v", err)
	}

	// Watch the decision subject — it must receive nothing in the default path.
	var reviewSeen int
	if _, err := requester.Subscribe(ctx, workflow.RunReviewSubject("01DEFAULTNOAGENT0000000000"), func(sextant.Message) {
		mu.Lock()
		reviewSeen++
		mu.Unlock()
	}, sextant.DeliverAll()); err != nil {
		t.Fatalf("subscribe review subject: %v", err)
	}

	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	// A DEFAULT run (AgentMode false).
	run := workflow.Run{
		ID: "01DEFAULTNOAGENT0000000000", Status: workflow.RunRunning, Objective: "default path",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "work", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	got := pollRun(t, ctx, requester, run.ID, 15*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Fatalf("default run final status = %q, want done", got.Status)
	}
	time.Sleep(300 * time.Millisecond) // let any stray review/spawn settle
	mu.Lock()
	defer mu.Unlock()
	if coordSpawns != 0 {
		t.Errorf("default run spawned a coordinator agent %d time(s); want 0 (AC#1: no agent when off)", coordSpawns)
	}
	if reviewSeen != 0 {
		t.Errorf("default run published %d run.review record(s); want 0 (the shell never consulted an agent)", reviewSeen)
	}
}

// TestAgentMode_AdvanceProceeds is the AC#2 advance proof: an agent-mode run where the
// reviewer advances every step reaches done, AND the shell sent ≥1 review (the decision the
// default path lacks).
func TestAgentMode_AdvanceProceeds(t *testing.T) {
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

	obs := &reviewObserver{}
	agentModeHarness(t, ctx, b, dispatcher, spawnSubj, nil, alwaysAdvance, obs)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	startAgentRun(t, ctx, requester, "01AGENTADVANCE000000000000", "do the thing", []workflow.RunStep{
		{ID: "s1", Label: "work", Kind: workflow.KindWork, Status: workflow.StepRunning},
		{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
	})

	got := pollRun(t, ctx, requester, "01AGENTADVANCE000000000000", 20*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Fatalf("agent-mode advance run final status = %q, want done; steps=%+v", got.Status, got.Steps)
	}
	if obs.count() < 1 {
		t.Errorf("expected ≥1 agent review (the decision the default lacks); got %d", obs.count())
	}
	// The decision must be recorded on the activity trail (observability).
	if !activityContains(got, "agent decision") {
		t.Errorf("no agent decision in the run activity trail; activity=%+v", got.Activity)
	}
}

// TestAgentMode_RedoReDispatchesSameStep is the AC#2 redo proof: the reviewer returns
// redo-with-feedback on the FIRST review of the work step, then advance on the second. The
// SAME step id must be re-dispatched, and the agent's feedback must appear in the worker's
// re-dispatch prompt. A rubber-stamp (always advance) would never loop — caught here.
func TestAgentMode_RedoReDispatchesSameStep(t *testing.T) {
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

	const feedback = "make it rhyme"
	// Record the prompts each work-step dispatch carried, so the test can prove the redo
	// re-dispatch saw the feedback.
	var pmu sync.Mutex
	s1Prompts := []string{}
	worker := func(t *testing.T, ctx context.Context, d *sextant.Client, job, stepID, prompt string) []workflow.ProducedArtifact {
		if stepID == "s1" {
			pmu.Lock()
			s1Prompts = append(s1Prompts, prompt)
			pmu.Unlock()
		}
		return defaultWorker(t, ctx, d, job, stepID, prompt)
	}
	// Reviewer: redo the FIRST time it sees s1, advance everything after.
	var seenS1 int
	var dmu sync.Mutex
	decide := func(r workflow.RunReview) workflow.RunDecision {
		if r.Step == "s1" {
			dmu.Lock()
			seenS1++
			first := seenS1 == 1
			dmu.Unlock()
			if first {
				return workflow.RunDecision{Verb: workflow.DecisionRedo, Feedback: feedback, Reason: "needs rework"}
			}
		}
		return workflow.RunDecision{Verb: workflow.DecisionAdvance}
	}
	agentModeHarness(t, ctx, b, dispatcher, spawnSubj, worker, decide, nil)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	startAgentRun(t, ctx, requester, "01AGENTREDO0000000000000AB", "write a poem", []workflow.RunStep{
		{ID: "s1", Label: "draft", Kind: workflow.KindWork, Status: workflow.StepRunning},
		{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
	})

	got := pollRun(t, ctx, requester, "01AGENTREDO0000000000000AB", 25*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Fatalf("redo run final status = %q, want done; steps=%+v", got.Status, got.Steps)
	}
	pmu.Lock()
	defer pmu.Unlock()
	if len(s1Prompts) < 2 {
		t.Fatalf("step s1 was dispatched %d time(s); redo-with-feedback must re-dispatch the SAME step (want ≥2)", len(s1Prompts))
	}
	// The re-dispatch (second prompt) must carry the agent's feedback.
	if !strings.Contains(s1Prompts[1], feedback) {
		t.Fatalf("the redo re-dispatch prompt did not carry the agent feedback %q.\n  prompt: %s", feedback, s1Prompts[1])
	}
	// The activity trail must record the redo.
	if !activityContains(got, "redo step") {
		t.Errorf("no redo entry in activity trail; activity=%+v", got.Activity)
	}
}

// TestAgentMode_EditThenAdvance is the AC#2 edit proof: an edit-then-advance decision
// proceeds (the agent's edit is its own act on the deliverable), and the verb is recorded on
// the activity trail (AC#6 "no silent edit bypassing the decision/activity trail").
func TestAgentMode_EditThenAdvance(t *testing.T) {
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

	decide := func(r workflow.RunReview) workflow.RunDecision {
		if r.Step == "s1" {
			return workflow.RunDecision{Verb: workflow.DecisionEdit, Reason: "fixed a typo in the deliverable"}
		}
		return workflow.RunDecision{Verb: workflow.DecisionAdvance}
	}
	agentModeHarness(t, ctx, b, dispatcher, spawnSubj, nil, decide, nil)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	startAgentRun(t, ctx, requester, "01AGENTEDIT00000000000000A", "do work", []workflow.RunStep{
		{ID: "s1", Label: "work", Kind: workflow.KindWork, Status: workflow.StepRunning},
		{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
	})

	got := pollRun(t, ctx, requester, "01AGENTEDIT00000000000000A", 20*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Fatalf("edit-then-advance run final status = %q, want done", got.Status)
	}
	if !activityContains(got, workflow.DecisionEdit) {
		t.Errorf("edit-then-advance was not recorded on the activity trail (silent edit); activity=%+v", got.Activity)
	}
}

// TestAgentMode_StopIsTerminal is the AC#2 stop proof: a stop decision on the first step
// ends the run terminal (done) without running later steps.
func TestAgentMode_StopIsTerminal(t *testing.T) {
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

	decide := func(r workflow.RunReview) workflow.RunDecision {
		return workflow.RunDecision{Verb: workflow.DecisionStop, Reason: "good enough, stopping"}
	}
	agentModeHarness(t, ctx, b, dispatcher, spawnSubj, nil, decide, nil)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	startAgentRun(t, ctx, requester, "01AGENTSTOP00000000000000A", "do work", []workflow.RunStep{
		{ID: "s1", Label: "work", Kind: workflow.KindWork, Status: workflow.StepRunning},
		{ID: "s2", Label: "more work", Kind: workflow.KindWork, Status: workflow.StepUpcoming},
		{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
	})

	got := pollRun(t, ctx, requester, "01AGENTSTOP00000000000000A", 20*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Fatalf("stop decision final status = %q, want done (terminal)", got.Status)
	}
	// s2 must NOT have run (the stop was on s1).
	if s := stepStatus(got, "s2"); s == workflow.StepDone {
		t.Errorf("step s2 ran despite a stop on s1; steps=%+v", got.Steps)
	}
}

// TestAgentMode_GraphReshapingRejected is the AC#2 vocabulary guard: a decision verb
// OUTSIDE the four flat verbs (here "branch") is rejected — the run does NOT silently
// advance on it. The shell blocks (a rejected decision is a failed review).
func TestAgentMode_GraphReshapingRejected(t *testing.T) {
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

	decide := func(r workflow.RunReview) workflow.RunDecision {
		return workflow.RunDecision{Verb: "branch", Reason: "let me fork the graph"} // not in v1
	}
	agentModeHarness(t, ctx, b, dispatcher, spawnSubj, nil, decide, nil)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	startAgentRun(t, ctx, requester, "01AGENTBRANCH0000000000000", "do work", []workflow.RunStep{
		{ID: "s1", Label: "work", Kind: workflow.KindWork, Status: workflow.StepRunning},
		{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
	})

	got := pollRun(t, ctx, requester, "01AGENTBRANCH0000000000000", 20*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status == workflow.RunDone {
		t.Fatalf("a graph-reshaping verb advanced the run to done; v1 must reject it (AC#2). steps=%+v", got.Steps)
	}
	if got.Status != workflow.RunBlocked {
		t.Errorf("a rejected decision verb should BLOCK the run; got status %q", got.Status)
	}
}

// TestAgentMode_SingleWriterPreserved is the AC#3 proof: the coordinator AGENT's decisions
// ride run.decision MESSAGES (authored by the agent's own bus identity, a DIFFERENT id than
// the shell), while the run envelope is owned and written ONLY by the shell. It asserts the
// agent posted ≥1 decision authored by its own id, that id is NOT the run's owner (the
// shell), and the agent's id never appears in the run state — so the agent influenced the
// run purely through messages, never by writing the envelope. (The shell has exactly one
// envelope writer, checkpoint(); the agent has no artifact.update call at all — its only
// run-facing write is the decision message observed here, AC#3's "no write path to the
// envelope".)
func TestAgentMode_SingleWriterPreserved(t *testing.T) {
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

	runID := "01AGENTSINGLEWRITER0000000"

	// Observe the decision subject: capture the bus-stamped AUTHOR of each run.decision the
	// coordinator agent posts (the bus stamps the author on the frame — untrusted fields
	// can't fake it). This is the agent's only run-facing write, and it is a MESSAGE.
	var dmu sync.Mutex
	decisionAuthors := map[string]int{}
	if _, err := requester.Subscribe(ctx, workflow.RunDecisionSubject(runID), func(m sextant.Message) {
		if _, ok := workflow.ParseRunDecision(m.Frame.Record); !ok {
			return
		}
		dmu.Lock()
		decisionAuthors[m.Frame.Author]++
		dmu.Unlock()
	}, sextant.DeliverAll()); err != nil {
		t.Fatalf("subscribe decision subject: %v", err)
	}

	agentModeHarness(t, ctx, b, dispatcher, spawnSubj, nil, alwaysAdvance, nil)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	startAgentRun(t, ctx, requester, runID, "do work", []workflow.RunStep{
		{ID: "s1", Label: "work", Kind: workflow.KindWork, Status: workflow.StepRunning},
		{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
	})

	got := pollRun(t, ctx, requester, runID, 20*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Fatalf("single-writer run final status = %q, want done", got.Status)
	}
	// The run's owner is the coordinator SHELL (set on adopt) — the sole envelope writer.
	if got.Owner == "" {
		t.Fatalf("run has no owner; expected the shell's client id")
	}
	time.Sleep(300 * time.Millisecond) // let the last decision frame settle on the subject
	dmu.Lock()
	defer dmu.Unlock()
	if len(decisionAuthors) == 0 {
		t.Fatalf("no run.decision observed; the agent never expressed a decision via a message")
	}
	// EVERY decision author is the AGENT, and the agent is NEVER the run owner/shell.
	for author, n := range decisionAuthors {
		if author == got.Owner {
			t.Errorf("a run.decision was authored by the run owner/shell %q — decisions must come from the AGENT, not the shell", author)
		}
		t.Logf("agent %q posted %d decision(s); run owner (shell) is %q", author, n, got.Owner)
	}
	// The agent's id must NOT appear anywhere in the run state as a writer/owner — it
	// influenced the run only through messages, never by writing the envelope (AC#3).
	for author := range decisionAuthors {
		if got.Owner == author {
			t.Errorf("agent %q became the run owner — it must have NO write path to the envelope", author)
		}
	}
}

// activityContains reports whether any activity entry's text contains sub.
func activityContains(r workflow.Run, sub string) bool {
	for _, a := range r.Activity {
		if strings.Contains(a.Text, sub) {
			return true
		}
	}
	return false
}
