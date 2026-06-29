package main

// TASK-244 proof tests: work-engine steps pipe REAL output, every work step's
// deliverable is captured as a distinct durable artifact, and the coordinator threads
// only artifact REFS (never reading their content). These are the adversarial proofs
// the strict ACs name — each fails on the pre-fix system:
//
//   - TestRun_PipesPriorStepOutput (AC#1): a 2-step produce→rewrite run where step 1's
//     deliverable carries a unique token and step 2 emits a DERIVATIVE that transforms
//     that token. The token-transform is the property — "step 2 ran" is not enough. On
//     the old workPrompt (objective + label only) step 2 never sees the token, so the
//     transformed token is absent and the test fails.
//   - TestRun_WorkStepWithNoArtifactBlocks (AC#2 negative): a work step that reports
//     done but attaches ZERO artifacts ends the run BLOCKED, not done (the 01KW8J2N
//     hollow case). The old code advanced silently.
//   - TestRun_DistinctArtifactPerWorkStep (AC#2 positive): a 2-work-step run attaches a
//     distinct artifact PER work step (counting the single brief artifact does not pass).
//   - TestRun_CoordinatorNeverReadsStepOutput (AC#3): on the work/thread path the
//     coordinator issues NO artifact_get against a step's produced artifact — it forwards
//     the ref and the downstream worker dereferences it.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/conventions/workflow/go"
	"github.com/love-lena/sextant/sdk/go"
)

// pipingDispatcher is a deterministic fake worker that models a real produce→rewrite
// pipeline. For each spawn.request it acks, then:
//   - a WORK step whose prompt lists NO input artifacts PRODUCES a fresh artifact whose
//     body embeds a unique token (token = "TOKEN-"+job+"-"+stepID); it reports that ref.
//   - a WORK step whose prompt DOES list input artifacts DEREFERENCES the first listed
//     input (artifact_get — the worker reads content, which is allowed; only the
//     coordinator must not), extracts the upstream token, and produces a DERIVATIVE
//     artifact that QUOTES the token wrapped as "REWRITE(<token>)". This is the piping
//     property: step 2's deliverable is a transform of step 1's real output, not a redo.
//   - a BRIEF step writes a clean brief artifact (no fabricated proof) with outcome done.
//
// The worker parses the INPUT ARTIFACTS block the coordinator threads (names after
// "- ", up to the first space), proving it operates on the threaded refs.
func pipingDispatcher(t *testing.T, ctx context.Context, d *sextant.Client, spawnSubj string) {
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
		case strings.Contains(req.Prompt, "stopping brief"):
			ev.Outcome = workflow.RunDone
			name := "brief.stopping." + req.Job
			if _, err := d.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"brief","outcome":"done","note":"work complete"}`)); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "stopping", Version: 1}}

		default: // a work step
			inputs := parseInputArtifacts(req.Prompt)
			name := fmt.Sprintf("deliverable.%s.%s", req.Job, stepID)
			var body string
			if len(inputs) == 0 {
				// Producer step: mint a fresh, unique token.
				token := "TOKEN-" + req.Job + "-" + stepID
				body = fmt.Sprintf(`{"$type":"poem","token":%q,"text":"a poem bearing %s"}`, token, token)
			} else {
				// Rewrite step: DEREFERENCE the threaded input, extract its token, and
				// emit a derivative that transforms it. (The worker reads content; only
				// the coordinator must not.)
				upstream, err := d.GetArtifact(ctx, inputs[0])
				if err != nil {
					return
				}
				var up struct {
					Token string `json:"token"`
				}
				_ = json.Unmarshal(upstream.Record, &up)
				body = fmt.Sprintf(`{"$type":"poem","derived_from":%q,"text":"REWRITE(%s)"}`, inputs[0], up.Token)
			}
			if _, err := d.CreateArtifact(ctx, name, json.RawMessage(body)); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "poem", Version: 1}}
		}
		_ = d.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal())
	}, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("pipingDispatcher Subscribe: %v", err)
	}
}

// parseInputArtifacts pulls the artifact names the coordinator threaded into the prompt
// under the "INPUT ARTIFACTS" block: lines beginning "- ", name up to the first space.
func parseInputArtifacts(prompt string) []string {
	i := strings.Index(prompt, "INPUT ARTIFACTS")
	if i < 0 {
		return nil
	}
	var out []string
	for _, line := range strings.Split(prompt[i:], "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		name := strings.TrimPrefix(line, "- ")
		if j := strings.IndexAny(name, " \t"); j >= 0 {
			name = name[:j]
		}
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// TestRun_PipesPriorStepOutput is the AC#1 piping proof: a produce→rewrite run where
// step 2's deliverable is a DERIVATIVE that quotes step 1's unique token. Asserts the
// token, minted only in step 1, appears transformed ("REWRITE(<token>)") in step 2's
// artifact — impossible unless step 2 actually saw step 1's real output.
func TestRun_PipesPriorStepOutput(t *testing.T) {
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

	pipingDispatcher(t, ctx, dispatcher, spawnSubj)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	runID := "01PIPE0000000000000000000A"
	run := workflow.Run{
		ID: runID, Status: workflow.RunRunning, Objective: "write then rewrite a poem",
		Steps: []workflow.RunStep{
			{ID: "produce", Label: "write the poem", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "rewrite", Label: "rewrite the poem", Kind: workflow.KindWork, Status: workflow.StepUpcoming},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	got := pollRun(t, ctx, requester, run.ID, 20*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Fatalf("final status = %q, want done; steps=%+v", got.Status, got.Steps)
	}

	// The rewrite step recorded a distinct produced artifact (its derivative).
	rewriteArt := producedByStep(got, "rewrite")
	if len(rewriteArt) == 0 {
		t.Fatalf("rewrite step recorded no produced artifact; steps=%+v", got.Steps)
	}
	// PROPERTY: step 2's deliverable transforms step 1's unique token. Fetch it and
	// assert the token (minted only in step 1) appears, wrapped as REWRITE(...).
	token := "TOKEN-" + runID + "-produce"
	art, err := requester.GetArtifact(ctx, rewriteArt[0].Name)
	if err != nil {
		t.Fatalf("get rewrite deliverable %q: %v", rewriteArt[0].Name, err)
	}
	want := "REWRITE(" + token + ")"
	if !strings.Contains(string(art.Record), want) {
		t.Fatalf("rewrite deliverable does not transform step 1's token.\n  want substring: %q\n  got record: %s\n(step 2 redid the work from scratch instead of piping step 1's output)", want, string(art.Record))
	}
}

// producedByStep returns the produced-artifact refs recorded on the run step with id.
func producedByStep(r workflow.Run, id string) []workflow.ProducedArtifact {
	for i := range r.Steps {
		if r.Steps[i].ID == id {
			return r.Steps[i].Produced
		}
	}
	return nil
}

// TestRun_WorkStepWithNoArtifactBlocks is the AC#2 negative proof: a work step that
// reports done but attaches NO artifact (output lived only in activity) must END the
// run BLOCKED, never advance to done — the 01KW8J2N hollow case.
func TestRun_WorkStepWithNoArtifactBlocks(t *testing.T) {
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

	// A worker that is hollow ONLY on the work step: it reports the work step done with
	// NO artifacts (the 01KW8J2N case — output lived only in agent.activity and was
	// lost), but writes a real brief artifact on the brief step. This isolates the AC#2
	// WORK-step gate: if the run still reaches done, the work-step hollow case slipped
	// through. (If the brief were also hollow, the pre-existing brief stop-gate would
	// block it for the wrong reason — a fake pass.)
	_, err = dispatcher.Subscribe(ctx, spawnSubj, func(m sextant.Message) {
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
		_ = dispatcher.Publish(ctx, spawnSubj, json.RawMessage(ackBytes))
		stepID := parseDirective(req.Prompt, "RUN_STEP")
		ev := workflow.RunEvent{Step: stepID, Status: workflow.StepDone}
		if strings.Contains(req.Prompt, "stopping brief") {
			// Real brief artifact, so the brief stop-gate is satisfied — only the work
			// step is hollow.
			ev.Outcome = workflow.RunDone
			name := "brief.stopping." + req.Job
			if _, err := dispatcher.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"brief","outcome":"done"}`)); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "stopping", Version: 1}}
		}
		// The WORK step reports done with NO artifacts — the hollow case.
		_ = dispatcher.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal())
	}, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("subscribe hollow dispatcher: %v", err)
	}
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	run := workflow.Run{
		ID: "01HOLLOW000000000000000000", Status: workflow.RunRunning, Objective: "produce something",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "do work", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	got := pollRun(t, ctx, requester, run.ID, 15*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunBlocked {
		t.Fatalf("a work step that produced no artifact must BLOCK the run; got status %q, steps=%+v", got.Status, got.Steps)
	}
	// And the run must NOT be done (no spurious success on a hollow step).
	if got.Status == workflow.RunDone {
		t.Fatalf("hollow work step advanced the run to done; steps=%+v", got.Steps)
	}
}

// TestRun_DistinctArtifactPerWorkStep is the AC#2 positive proof: a 2-work-step run
// attaches a DISTINCT artifact per work step. Guards the fake-pass: counting the single
// brief artifact for both work steps must not pass — each work step records its own.
func TestRun_DistinctArtifactPerWorkStep(t *testing.T) {
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

	pipingDispatcher(t, ctx, dispatcher, spawnSubj)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	run := workflow.Run{
		ID: "01DISTINCT0000000000000000", Status: workflow.RunRunning, Objective: "two distinct deliverables",
		Steps: []workflow.RunStep{
			{ID: "stepA", Label: "first deliverable", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "stepB", Label: "second deliverable", Kind: workflow.KindWork, Status: workflow.StepUpcoming},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	got := pollRun(t, ctx, requester, run.ID, 20*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Fatalf("final status = %q, want done; steps=%+v", got.Status, got.Steps)
	}

	a := producedByStep(got, "stepA")
	bArts := producedByStep(got, "stepB")
	if len(a) == 0 {
		t.Errorf("stepA recorded no produced artifact")
	}
	if len(bArts) == 0 {
		t.Errorf("stepB recorded no produced artifact")
	}
	if len(a) > 0 && len(bArts) > 0 && a[0].Name == bArts[0].Name {
		t.Errorf("the two work steps share one artifact %q; want a DISTINCT artifact per work step", a[0].Name)
	}
	// Both work-step deliverables (distinct from the brief) must be attached to the run.
	names := map[string]bool{}
	for _, art := range got.Artifacts {
		names[art.Name] = true
	}
	if len(a) > 0 && !names[a[0].Name] {
		t.Errorf("stepA deliverable %q not attached to the run; attached=%+v", a[0].Name, got.Artifacts)
	}
	if len(bArts) > 0 && !names[bArts[0].Name] {
		t.Errorf("stepB deliverable %q not attached to the run; attached=%+v", bArts[0].Name, got.Artifacts)
	}
}

// TestRun_CoordinatorNeverReadsStepOutput is the AC#3 content-opacity proof. It installs
// a recorder on every coordinator's artifact seams and runs the same produce→rewrite
// piping run. The coordinator's job is to THREAD the ref (name) into step 2's prompt; the
// WORKER dereferences it. So across the whole run the coordinator must NEVER read a work
// step's produced artifact's CONTENT — asserted directly against the content-read log.
//
// The proof gate (TASK-243) existence-PROBES every step's reported artifacts; that is
// metadata (it discards the body), recorded on a separate seam, and explicitly allowed —
// the test asserts the work-step deliverables WERE existence-probed but their content was
// NOT read, drawing the content-opacity line exactly where it belongs.
func TestRun_CoordinatorNeverReadsStepOutput(t *testing.T) {
	log, restore := ArtifactReadRecorder()
	t.Cleanup(restore)

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

	pipingDispatcher(t, ctx, dispatcher, spawnSubj)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	runID := "01OPAQUE00000000000000000A"
	run := workflow.Run{
		ID: runID, Status: workflow.RunRunning, Objective: "write then rewrite",
		Steps: []workflow.RunStep{
			{ID: "produce", Label: "write", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "rewrite", Label: "rewrite", Kind: workflow.KindWork, Status: workflow.StepUpcoming},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	got := pollRun(t, ctx, requester, run.ID, 20*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Fatalf("final status = %q, want done; steps=%+v", got.Status, got.Steps)
	}

	// The work steps' produced artifacts — the coordinator must have threaded these as
	// refs WITHOUT reading their content.
	produceArt := producedByStep(got, "produce")
	rewriteArt := producedByStep(got, "rewrite")
	if len(produceArt) == 0 || len(rewriteArt) == 0 {
		t.Fatalf("expected both work steps to record a deliverable; produce=%+v rewrite=%+v", produceArt, rewriteArt)
	}
	for _, a := range append(produceArt, rewriteArt...) {
		if log.Read(a.Name) {
			t.Errorf("coordinator READ work-step output artifact %q's CONTENT — content-opacity violated (AC#3). It must thread the ref only and let the worker dereference it.\n  content reads: %v", a.Name, log.Names())
		}
		// The proof gate must have existence-PROBED the deliverable (metadata, body
		// discarded) — proving every step's reported artifacts are verified to exist
		// (TASK-243) without reading their content.
		if !log.ExistsProbed(a.Name) {
			t.Errorf("coordinator did not existence-probe work-step artifact %q — the TASK-243 per-step existence gate did not run on it", a.Name)
		}
	}
}
