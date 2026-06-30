package main

// Integration tests for the run executor (TASK-236, ADR-0048) over a REAL bus.
//
// The coordinator adopts a sextant.workflow.run/v1 run the requester (the dash)
// wrote, then drives its steps to a terminal status. A test-side dispatcher
// cooperates: for every spawn.request it publishes a spawn.ack, then a run.event
// step-done on the run's events subject — echoing the step id the coordinator asked
// for (parsed from the prompt's RUN_STEP directive), and attaching a brief artifact
// for the brief step. This matches what the M5.2 dispatcher + a worker do in
// production — the coordinator is not faked.
//
// Coverage:
//   - TestRun_AdvancesToBrief: work → brief advances to done; brief attached; activity.
//   - TestRun_CheckpointWaitsForApprove: a checkpoint parks at waiting → approve → done.
//   - TestRun_CancelHalts: a run.control cancel drives the run to cancelled.
//   - TestStartConsumer_FreshStartAcks: run.start → ok ack echoing id + nonce.
//   - TestStartConsumer_IgnoresHistoricalStart: TASK-192 — new-only delivery, no replay.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/conventions/workflow/go"
	"github.com/love-lena/sextant/sdk/go"
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

// parseDirective pulls "KEY=value" out of a prompt (value ends at whitespace).
func parseDirective(prompt, key string) string {
	i := strings.Index(prompt, key+"=")
	if i < 0 {
		return ""
	}
	rest := prompt[i+len(key)+1:]
	if j := strings.IndexAny(rest, " \n\t"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// cooperatingDispatcher subscribes to the spawn subject and, for every spawn.request,
// publishes a spawn.ack then a run.event step-done (echoing RUN_STEP, with a brief
// artifact + outcome for the brief step). delay defers the step-done so a timing
// assertion can observe ack-before-done.
func cooperatingDispatcher(t *testing.T, ctx context.Context, d *sextant.Client, spawnSubj string, delay time.Duration) {
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
		if strings.Contains(req.Prompt, "stopping brief") {
			ev.Outcome = workflow.RunDone
			// The real pi worker actually PUTS the brief artifact; the fake must too,
			// or the coordinator's existence-gate (it confirms every declared artifact
			// exists) would correctly block. This clean brief claims no proof artifact.
			// A NON-brief kind ("stopping") proves the gate keys on the step boundary +
			// existence, not the model's arbitrary kind label.
			name := "brief.stopping." + req.Job
			if _, err := d.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"brief","outcome":"done","note":"work complete"}`)); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "stopping", Version: 1}}
		} else {
			// A WORK step's worker PRODUCES a durable deliverable (TASK-244 AC#2: a work
			// step that captures no artifact is the 01KW8J2N hollow case and blocks the
			// run). Name it per step so distinct steps yield distinct artifacts.
			name := "deliverable." + req.Job + "." + stepID
			if _, err := d.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"work","step":"`+stepID+`"}`)); err != nil {
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
		t.Fatalf("dispatcher Subscribe: %v", err)
	}
}

// writeRunAndStart writes the run artifact (the dash's spawn act) then publishes a
// run.start to wake the coordinator. Returns the run id and the start frame id.
func writeRunAndStart(t *testing.T, ctx context.Context, requester *sextant.Client, run workflow.Run, nonce string) (string, string) {
	t.Helper()
	if _, err := requester.CreateArtifact(ctx, workflow.RunStateName(run.ID), run.Marshal()); err != nil {
		t.Fatalf("create run artifact: %v", err)
	}
	out, err := requester.PublishMsg(ctx, workflow.RunStartSubject, workflow.RunStartRecord(workflow.RunStartRequest{ID: run.ID, Nonce: nonce}))
	if err != nil {
		t.Fatalf("publish run.start: %v", err)
	}
	return run.ID, out.ID
}

// pollRun reads the run artifact until pred holds or the deadline elapses.
func pollRun(t *testing.T, ctx context.Context, c *sextant.Client, runID string, deadline time.Duration, pred func(workflow.Run) bool) workflow.Run {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		art, err := c.GetArtifact(ctx, workflow.RunStateName(runID))
		if err == nil {
			if r, ok := workflow.ParseRun(art.Record); ok && pred(r) {
				return r
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	art, _ := c.GetArtifact(ctx, workflow.RunStateName(runID))
	r, _ := workflow.ParseRun(art.Record)
	t.Fatalf("run %s did not reach the expected state within %s; last: status=%q steps=%+v", runID, deadline, r.Status, r.Steps)
	return r
}

func startListenConsumer(t *testing.T, ctx context.Context, consumer *sextant.Client, spawnSubj string, stepTimeout time.Duration) {
	t.Helper()
	// These tests are repo-less (no Run.Repo): an empty store provisions no worktree,
	// preserving the scratch-default path. A worktree-specific test (worktree_test.go)
	// calls newStartConsumer directly with a real store.
	_, sub, err := newStartConsumer(ctx, consumer, spawnSubj, stepTimeout, "")
	if err != nil {
		t.Fatalf("newStartConsumer: %v", err)
	}
	t.Cleanup(sub.Stop)
}

// TestRun_AdvancesToBrief covers AC#1–#4: a dash-spawned run with a work step and a
// terminal brief advances on its own to done, attaching the brief and recording activity.
func TestRun_AdvancesToBrief(t *testing.T) {
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

	cooperatingDispatcher(t, ctx, dispatcher, spawnSubj, 0)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	run := workflow.Run{
		ID: "01RUNADV", Status: workflow.RunRunning, Objective: "do the thing",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "investigate", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	got := pollRun(t, ctx, requester, run.ID, 15*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Errorf("final status = %q, want done", got.Status)
	}
	if got.Steps[0].Status != workflow.StepDone {
		t.Errorf("s1 status = %q, want done", got.Steps[0].Status)
	}
	if len(got.Artifacts) == 0 {
		t.Errorf("no artifact attached from the brief step; artifacts=%+v", got.Artifacts)
	}
	if len(got.Activity) < 2 {
		t.Errorf("want ≥2 activity entries (step done + terminal), got %d", len(got.Activity))
	}
}

// COVERAGE SHIFT (TASK-243 option A) — read before the three tests below.
//
// Proof for the deterministic coordinator gate is now the TYPED produced-artifact
// metadata the worker reports (RunEvent.Artifacts, collected mechanically by the
// worker's runtime), existence-checked on the bus — NOT the brief's prose. The gate no
// longer parses the brief body for proof refs under any key. Consequence, stated plainly
// so this is not a silent weakening: a deliverable named ONLY in brief prose (with no
// corresponding typed produced-artifact) is intentionally NO LONGER gated by the
// deterministic coordinator — that is content-opacity, and judging whether the brief's
// prose truthfully describes a real deliverable is the opt-in agent-mode reviewer's job
// (TASK-242). A brief-only run whose prose makes a false narrative claim can reach done;
// that is acceptable BY DESIGN, because the brief artifact IS the run's real deliverable
// and narrative accuracy is the reviewer's scope, not the gate's.
//
// The original 01KW8J2N fabrication class is closed — by the COMBINATION of two distinct
// gates applied to EVERY step (work AND brief), which these tests exercise together:
//   - The COUNT gate (TASK-244 hollow-step, piping_test.go: TestRun_WorkStepWithNoArtifactBlocks):
//     a step that reports done but NAMES no artifact blocks (its output was never captured).
//   - The EXISTENCE gate (TASK-243, this file + the work-step path): every artifact a
//     step's worker REPORTS producing must actually exist on the bus, else the step blocks.
//     Applied to BOTH work steps (TestRun_BlocksOnWorkStepFabricatedArtifact) and the brief
//     (TestRun_BlocksOnFabricatedProof) — a phantom ref blocks at ANY step, not only the brief.
// Together, on every step: no hollow step AND no phantom ref → a run cannot reach done over
// a non-existent deliverable at ANY step. Neither gate alone suffices; both are required,
// and the existence gate is required on EVERY step (counting only at the brief was the
// PROBE-A hole). Whether a brief's prose truthfully describes the deliverable remains the
// TASK-242 reviewer's content job, not the deterministic gate's.

// TestRun_BlocksOnFabricatedProof is the TASK-243 stop-gate property (AC1/AC3,
// shape-independent): a brief whose worker REPORTS producing a proof artifact that was
// never created on the bus must NOT reach done — the run blocks. Proves the coordinator
// verifies the worker's typed produced-artifact metadata INDEPENDENTLY (it fetches each
// from the bus) instead of trusting the worker's say-so. This is the live 01KW8J2N
// fabrication reproduced deterministically, now expressed in the TYPED proof channel —
// no brief-body key is involved, so the gate's behaviour does not depend on parsing the
// brief at all.
//
// The work step PRODUCES a real artifact, so the run actually reaches the brief gate
// (rather than blocking earlier on TASK-244's hollow-step gate) — this isolates the
// brief proof gate as the reason for the block.
func TestRun_BlocksOnFabricatedProof(t *testing.T) {
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

	// Work step: produce a real deliverable (passes the hollow-step gate, so the run
	// reaches the brief). Brief step: create a real brief artifact, but REPORT producing
	// a second artifact (phantom-<job>) that it never actually created — the fabrication.
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
			ev.Outcome = workflow.RunDone
			name := "brief.fab." + req.Job
			if _, err := dispatcher.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"brief","outcome":"done"}`)); err != nil {
				return
			}
			// Report producing BOTH the brief AND a phantom proof artifact that was never
			// created. The gate existence-checks every reported ref → the phantom blocks.
			ev.Artifacts = []workflow.ProducedArtifact{
				{Name: name, Kind: "brief", Version: 1},
				{Name: "phantom-" + req.Job, Kind: "poem", Version: 1},
			}
		} else {
			name := "deliverable." + req.Job + "." + stepID
			if _, err := dispatcher.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"work"}`)); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "work", Version: 1}}
		}
		_ = dispatcher.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal())
	})
	if err != nil {
		t.Fatalf("subscribe dispatcher: %v", err)
	}
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	run := workflow.Run{
		ID: "01FABPROOF0000000000000000", Status: workflow.RunRunning, Objective: "write a poem",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "investigate", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	got := pollRun(t, ctx, requester, run.ID, 15*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunBlocked {
		t.Errorf("a brief reporting a produced proof artifact that does not exist must BLOCK; got status %q", got.Status)
	}
	// It must block at the BRIEF (the proof gate), proving it reached the gate — not
	// earlier on the hollow-step gate (the work step produced a real artifact).
	if s := stepStatus(got, "s1"); s != workflow.StepDone {
		t.Errorf("work step s1 should have completed (it produced a real artifact); got status %q — the run blocked before reaching the brief gate, so this does not exercise the proof gate", s)
	}
}

// TestRun_BlocksOnWorkStepFabricatedArtifact is the TASK-243 PROBE A regression: the
// existence gate must apply to EVERY step, not only the brief. A WORK step reports
// producing a typed artifact (phantom-work-<job>) that was never created on the bus; the
// hollow-step COUNT gate passes (the worker named one), but the EXISTENCE gate must catch
// the phantom and BLOCK the run — the 01KW8J2N fabrication class relocated from the brief
// to a work step. (RED before the per-step existence check: the run reaches done.)
func TestRun_BlocksOnWorkStepFabricatedArtifact(t *testing.T) {
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

	// Work step: report producing phantom-work-<job> WITHOUT creating it (passes the count
	// gate, must fail the existence gate). Brief step: create a real brief and report only
	// it, so if the run somehow reached the brief the brief gate would NOT be the blocker —
	// isolating the WORK-step existence gate as the only thing that can block this run.
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
			ev.Outcome = workflow.RunDone
			name := "brief.real." + req.Job
			if _, err := dispatcher.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"brief","outcome":"done"}`)); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "brief", Version: 1}}
		} else {
			// WORK step: report a produced artifact that is NEVER created on the bus.
			ev.Artifacts = []workflow.ProducedArtifact{{Name: "phantom-work-" + req.Job, Kind: "poem", Version: 1}}
		}
		_ = dispatcher.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal())
	})
	if err != nil {
		t.Fatalf("subscribe dispatcher: %v", err)
	}
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	run := workflow.Run{
		ID: "01WORKFAB00000000000000000", Status: workflow.RunRunning, Objective: "write a poem",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "write the poem", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	got := pollRun(t, ctx, requester, run.ID, 15*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunBlocked {
		t.Errorf("a WORK step reporting a produced artifact that does not exist must BLOCK the run (per-step existence gate); got status %q, steps=%+v", got.Status, got.Steps)
	}
	// The phantom is a work-step ref, so the run must block at s1 — it must NOT have
	// advanced past the work step or reached done.
	if got.Status == workflow.RunDone {
		t.Errorf("a work-step phantom artifact advanced the run to done; the existence gate did not run on the work step. steps=%+v", got.Steps)
	}
}

// stepStatus returns the status of the run step with id (empty if absent).
func stepStatus(r workflow.Run, id string) string {
	for i := range r.Steps {
		if r.Steps[i].ID == id {
			return r.Steps[i].Status
		}
	}
	return ""
}

// TestRun_DoneWithDeliverableUnderNovelBriefKey is the TASK-243 AC4 shape-independence
// proof: a run whose brief describes its deliverable under a NOVEL, unrecognized brief-
// body key (here a free-text `poem_text` and a `deliverables` array — none of the keys
// the old body-parse hardcoded) reaches DONE, because the gate decides SOLELY from the
// worker's typed produced-artifact metadata (every reported ref exists) and never reads
// the brief body. The fake-pass guard for the inverse: the old code could only have
// passed this by hardcoding the novel key — there is no longer any key set to add to, so
// the gate is independent of brief shape entirely.
//
// The work step and the brief both produce REAL artifacts (so the hollow-step and the
// existence gates are satisfied); the only "fabrication risk" here is the brief body's
// novel-keyed prose, which the gate must simply ignore.
func TestRun_DoneWithDeliverableUnderNovelBriefKey(t *testing.T) {
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
			ev.Outcome = workflow.RunDone
			name := "brief.novelkey." + req.Job
			// The brief describes its deliverable under NOVEL keys the old body-parse never
			// recognized (poem_text free-text + a deliverables[] array). The deliverable IS
			// a real artifact (deliverable.<job>.s1), produced by the work step and reported
			// in the typed metadata — but it is NOT named under any key the old gate knew.
			rec := json.RawMessage(`{"$type":"brief","outcome":"done","poem_text":"roses are red","deliverables":[{"ref":"deliverable.` + req.Job + `.s1"}]}`)
			if _, err := dispatcher.CreateArtifact(ctx, name, rec); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "brief", Version: 1}}
		} else {
			name := "deliverable." + req.Job + "." + stepID
			if _, err := dispatcher.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"poem","text":"roses are red"}`)); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "poem", Version: 1}}
		}
		_ = dispatcher.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal())
	})
	if err != nil {
		t.Fatalf("subscribe dispatcher: %v", err)
	}
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	run := workflow.Run{
		ID: "01NOVELKEY0000000000000000", Status: workflow.RunRunning, Objective: "write a poem",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "write the poem", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	got := pollRun(t, ctx, requester, run.ID, 15*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Errorf("a run whose deliverable exists but is described under a novel brief-body key must reach done (the gate ignores brief shape); got status %q, steps=%+v", got.Status, got.Steps)
	}
}

// TestRun_BlocksOnFabricatedProofUnderNovelKey is the TASK-243 AC4 adversarial blocking
// proof: a brief that names its deliverable under an UNRECOGNIZED prose key AND reports
// producing it in the typed metadata, where that artifact was NEVER created, must BLOCK.
// This is the key-drift failure class (01KW8J2N on a novel key): the deliverable name
// drifts to a key the old body-parse would skip, so the old gate would let the run reach
// done. Under option A the gate ignores the key entirely and existence-checks the typed
// ref → blocks regardless of the brief's shape. The work step produces a real artifact so
// the run reaches the brief gate (isolating it from the hollow-step gate).
func TestRun_BlocksOnFabricatedProofUnderNovelKey(t *testing.T) {
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
			ev.Outcome = workflow.RunDone
			name := "brief.novelfab." + req.Job
			phantom := "poem-for-next-week-" + req.Job
			// Deliverable named ONLY under a novel free-text key the old body-parse skips —
			// AND reported as produced in the typed metadata, but never actually created.
			rec := json.RawMessage(`{"$type":"brief","outcome":"done","poem_text":"the poem lives only here","my_deliverable":"` + phantom + `"}`)
			if _, err := dispatcher.CreateArtifact(ctx, name, rec); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{
				{Name: name, Kind: "brief", Version: 1},
				{Name: phantom, Kind: "poem", Version: 1}, // never created → must block
			}
		} else {
			name := "deliverable." + req.Job + "." + stepID
			if _, err := dispatcher.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"work"}`)); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "work", Version: 1}}
		}
		_ = dispatcher.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal())
	})
	if err != nil {
		t.Fatalf("subscribe dispatcher: %v", err)
	}
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	run := workflow.Run{
		ID: "01NOVELFAB0000000000000000", Status: workflow.RunRunning, Objective: "write a poem for next week",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "draft", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	got := pollRun(t, ctx, requester, run.ID, 15*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunBlocked {
		t.Errorf("a brief whose reported deliverable does not exist must BLOCK even when named under a novel brief-body key (key-drift class); got status %q", got.Status)
	}
	if s := stepStatus(got, "s1"); s != workflow.StepDone {
		t.Errorf("work step s1 should have completed; got %q — run blocked before the brief gate, not exercising the proof gate", s)
	}
}

// TestRun_CheckpointWaitsForApprove covers TASK-225: a checkpoint step parks the run
// at waiting until an operator run.control approve, then it advances to done.
func TestRun_CheckpointWaitsForApprove(t *testing.T) {
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

	cooperatingDispatcher(t, ctx, dispatcher, spawnSubj, 0)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	run := workflow.Run{
		ID: "01RUNCHK", Status: workflow.RunRunning, Objective: "needs a gate",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "approve me", Kind: workflow.KindCheckpoint, Status: workflow.StepUpcoming},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	// Parks at waiting.
	pollRun(t, ctx, requester, run.ID, 10*time.Second, func(r workflow.Run) bool {
		return r.Status == workflow.RunWaiting && r.Steps[0].Status == workflow.StepWaiting
	})

	// Operator approves.
	if err := requester.Publish(ctx, workflow.RunControlSubject(run.ID),
		(workflow.RunControl{Verb: workflow.CtlApprove}).Marshal()); err != nil {
		t.Fatalf("publish approve: %v", err)
	}

	got := pollRun(t, ctx, requester, run.ID, 15*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Errorf("final status = %q, want done", got.Status)
	}
	if got.Steps[0].Status != workflow.StepDone {
		t.Errorf("checkpoint step status = %q, want done", got.Steps[0].Status)
	}
}

// TestRun_CancelHalts covers TASK-226: a run.control cancel while a run is waiting at
// a checkpoint drives it to cancelled.
func TestRun_CancelHalts(t *testing.T) {
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

	cooperatingDispatcher(t, ctx, dispatcher, spawnSubj, 0)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	run := workflow.Run{
		ID: "01RUNCAN", Status: workflow.RunRunning, Objective: "cancel me",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "wait here", Kind: workflow.KindCheckpoint, Status: workflow.StepUpcoming},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	pollRun(t, ctx, requester, run.ID, 10*time.Second, func(r workflow.Run) bool {
		return r.Status == workflow.RunWaiting
	})

	if err := requester.Publish(ctx, workflow.RunControlSubject(run.ID),
		(workflow.RunControl{Verb: workflow.CtlCancel}).Marshal()); err != nil {
		t.Fatalf("publish cancel: %v", err)
	}

	got := pollRun(t, ctx, requester, run.ID, 10*time.Second, func(r workflow.Run) bool {
		return r.Status == workflow.RunCancelled
	})
	sawCancel := false
	for _, a := range got.Activity {
		if strings.Contains(a.Text, "cancel") {
			sawCancel = true
		}
	}
	if !sawCancel {
		t.Errorf("no cancel activity entry; activity=%+v", got.Activity)
	}
	// A cancelled checkpoint must NOT be recorded as done (no spurious ✓): the dash
	// would otherwise render a stopped checkpoint as successfully approved.
	if got.Steps[0].Status == workflow.StepDone {
		t.Errorf("cancelled checkpoint step recorded as done; steps=%+v", got.Steps)
	}
}

// TestRun_CancelDuringWork covers cancel responsiveness mid-step: a cancel published
// while a work step is dispatched (the coordinator blocked in awaitStepDone) aborts
// the wait PROMPTLY and finishes the run cancelled — not blocked after step-timeout.
func TestRun_CancelDuringWork(t *testing.T) {
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

	// The dispatcher acks but defers the step-done far beyond the cancel window, so
	// the coordinator sits in awaitStepDone when the cancel lands.
	cooperatingDispatcher(t, ctx, dispatcher, spawnSubj, 30*time.Second)
	// A generous step timeout so a slow cancel would be obvious (cancel must beat it).
	startListenConsumer(t, ctx, consumer, spawnSubj, 60*time.Second)

	run := workflow.Run{
		ID: "01RUNCDW", Status: workflow.RunRunning, Objective: "cancel mid-work",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "long work", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	// Wait until s1 is actually running (dispatched), then cancel.
	pollRun(t, ctx, requester, run.ID, 10*time.Second, func(r workflow.Run) bool {
		return r.Steps[0].Status == workflow.StepRunning && r.Status == workflow.RunRunning
	})
	if err := requester.Publish(ctx, workflow.RunControlSubject(run.ID),
		(workflow.RunControl{Verb: workflow.CtlCancel}).Marshal()); err != nil {
		t.Fatalf("publish cancel: %v", err)
	}

	// Cancel must take well within the 30s dispatcher delay / 60s step timeout.
	got := pollRun(t, ctx, requester, run.ID, 10*time.Second, func(r workflow.Run) bool {
		return r.Status == workflow.RunCancelled
	})
	if got.Status != workflow.RunCancelled {
		t.Errorf("final status = %q, want cancelled", got.Status)
	}
}

// TestStartConsumer_FreshStartAcks: a run.start for a written run gets an ok ack
// echoing the run id + nonce, before the run completes.
func TestStartConsumer_FreshStartAcks(t *testing.T) {
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

	// Delay step-done so the ack must arrive before the run completes.
	cooperatingDispatcher(t, ctx, dispatcher, spawnSubj, 1500*time.Millisecond)
	startListenConsumer(t, ctx, consumer, spawnSubj, 10*time.Second)

	var (
		ackMu sync.Mutex
		acks  []workflow.RunStartAck
	)
	if _, err := requester.Subscribe(ctx, workflow.RunStartSubject, func(m sextant.Message) {
		var a workflow.RunStartAck
		if err := json.Unmarshal(m.Frame.Record, &a); err != nil || a.Type != workflow.TypeRunStartAck {
			return
		}
		ackMu.Lock()
		acks = append(acks, a)
		ackMu.Unlock()
	}, sextant.DeliverAll()); err != nil {
		t.Fatalf("subscribe acks: %v", err)
	}

	run := workflow.Run{
		ID: "01RUNACK", Status: workflow.RunRunning, Objective: "ack me",
		Steps: []workflow.RunStep{{ID: "s1", Kind: workflow.KindWork, Status: workflow.StepRunning}},
	}
	_, _ = writeRunAndStart(t, ctx, requester, run, "nonce-xyz")

	deadline := time.Now().Add(750 * time.Millisecond) // < the 1.5s step delay
	for time.Now().Before(deadline) {
		ackMu.Lock()
		n := len(acks)
		ackMu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	ackMu.Lock()
	defer ackMu.Unlock()
	if len(acks) != 1 {
		t.Fatalf("want exactly 1 ack before the run completes, got %d", len(acks))
	}
	if acks[0].Status != workflow.StatusOK {
		t.Errorf("ack.status = %q, want ok (error: %s)", acks[0].Status, acks[0].Error)
	}
	if acks[0].ID != run.ID {
		t.Errorf("ack.id = %q, want %q", acks[0].ID, run.ID)
	}
	if acks[0].Nonce != "nonce-xyz" {
		t.Errorf("ack.nonce = %q, want nonce-xyz", acks[0].Nonce)
	}
}

// TestStartConsumer_IgnoresHistoricalStart is the TASK-192 regression: new-only
// delivery means a run.start published before the consumer subscribes is ignored.
func TestStartConsumer_IgnoresHistoricalStart(t *testing.T) {
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	requester := dialBusClient(t, b, "requester")
	consumer := dialBusClient(t, b, "consumer")

	// A HISTORICAL run.start, published before any consumer exists (its run artifact
	// need not exist — a replayed start would still produce an ack if wrongly seen).
	if _, err := requester.PublishMsg(ctx, workflow.RunStartSubject,
		workflow.RunStartRecord(workflow.RunStartRequest{ID: "01HIST"})); err != nil {
		t.Fatalf("publish historical start: %v", err)
	}

	var (
		ackMu sync.Mutex
		acks  []workflow.RunStartAck
	)
	if _, err := requester.Subscribe(ctx, workflow.RunStartSubject, func(m sextant.Message) {
		var a workflow.RunStartAck
		if err := json.Unmarshal(m.Frame.Record, &a); err != nil || a.Type != workflow.TypeRunStartAck {
			return
		}
		ackMu.Lock()
		acks = append(acks, a)
		ackMu.Unlock()
	}); err != nil {
		t.Fatalf("subscribe acks: %v", err)
	}

	startListenConsumer(t, ctx, consumer, "msg.topic.spawn", 2*time.Second)

	time.Sleep(1500 * time.Millisecond)
	ackMu.Lock()
	nHist := len(acks)
	ackMu.Unlock()
	if nHist != 0 {
		t.Fatalf("consumer replayed a historical run.start (%d ack(s), want 0) — DeliverAll regression (TASK-192)", nHist)
	}
}
