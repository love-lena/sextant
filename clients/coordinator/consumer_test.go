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
			ev.Artifacts = []workflow.ProducedArtifact{{Name: "brief-" + req.Job, Kind: "brief", Version: 1}}
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
	_, sub, err := newStartConsumer(ctx, consumer, spawnSubj, stepTimeout)
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
	hasBrief := false
	for _, a := range got.Artifacts {
		if a.Kind == "brief" {
			hasBrief = true
		}
	}
	if !hasBrief {
		t.Errorf("no brief artifact attached; artifacts=%+v", got.Artifacts)
	}
	if len(got.Activity) < 2 {
		t.Errorf("want ≥2 activity entries (step done + terminal), got %d", len(got.Activity))
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
