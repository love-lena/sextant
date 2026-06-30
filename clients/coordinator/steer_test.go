package main

// TASK-246 proof: an operator message on the run topic (msg.topic.run.<id>) demonstrably
// STEERS the active run. Today such a post reaches no worker — it is silently ignored
// (the 01KW8J2N case: an operator posted "Write the poem to its own artifact" and no
// worker ever saw it). The fix: the coordinator subscribes the run topic and, on an
// operator chat.message, records the steer on the run AND routes it to the active step's
// worker (a DM to its inbox), so the worker incorporates it WITHIN the active run.
//
// Three adversarial proofs, each RED on the pre-fix coordinator (or its leg):
//   - TestRun_OperatorSteerInfluencesActiveRun: an operator posts a steer DURING an
//     active work step; a worker that acts ONLY on the coordinator-routed steer produces
//     the steered artifact, and the run's activity REFERENCES the operator's message.
//     Both the artifact and the activity reference are impossible on the pre-fix code
//     (no run-topic subscription, no routing) — the steer reached nothing.
//   - TestRun_SteerThreadsIntoNextStepPrompt: a steer that lands BETWEEN steps (during
//     step N, before step N+1 is dispatched) appears in step N+1's worker prompt — the
//     step-boundary leg the LIVE case relies on (a real worker often isn't mid-step when
//     the steer arrives). RED if the steerHistory→workPrompt threading is removed.
//   - TestRun_SteerAfterTerminalReportedNotApplied: a steer posted AFTER the run is
//     terminal is reported not-applied on the run topic, NEVER silently dropped.
//
// The worker fake is deliberately built so a steer that fails to ARRIVE leaves a
// detectable hole (no steered artifact, no activity reference) — a thread that merely
// LOOKS like a chat input but reaches nothing FAILS the test (the fake-pass guard).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/conventions/workflow/go"
	"github.com/love-lena/sextant/protocol/sx"
	"github.com/love-lena/sextant/sdk/go"
)

// steerableDispatcher is a fake dispatcher+worker that models a worker which is RESIDENT
// for its work step and incorporates an operator steer the COORDINATOR routes to its
// inbox (msg.client.<agentID>) — exactly the live mid-step path (TASK-246). For the work
// step it does NOT report done immediately: it acks, subscribes the agent's inbox, and
// waits (bounded) for the coordinator-routed steer. On the steer it creates a "steered"
// artifact whose body QUOTES the operator's text, then reports the step done carrying
// that artifact. The brief step writes a clean brief and reports done.
//
// The waited-for steer is the property under test: with no coordinator routing the inbox
// frame never arrives, the wait times out, and the work step reports NO steered artifact
// (and the run never records the steer) — the pre-fix silent-ignore, caught RED.
//
// steeredCh is signalled with the steered artifact name once the worker acts on a steer,
// so the test can assert the worker genuinely received and acted on the operator message.
func steerableDispatcher(t *testing.T, ctx context.Context, d *sextant.Client, spawnSubj string, steeredCh chan string) {
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
		agentID := "agent-" + m.Frame.ID[:8]
		ack := workflow.SpawnAck{Type: workflow.TypeSpawnAck, ID: agentID, RequestID: m.Frame.ID, Status: workflow.StatusOK}
		ackBytes, _ := json.Marshal(ack)
		if err := d.Publish(ctx, spawnSubj, json.RawMessage(ackBytes)); err != nil {
			return
		}
		stepID := parseDirective(req.Prompt, "RUN_STEP")
		ev := workflow.RunEvent{Step: stepID, Status: workflow.StepDone}

		if strings.Contains(req.Prompt, "stopping brief") {
			ev.Outcome = workflow.RunDone
			name := "brief.stopping." + req.Job
			if _, err := d.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"brief","outcome":"done"}`)); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "stopping", Version: 1}}
			_ = d.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal())
			return
		}

		// A WORK step. The worker is resident: it waits for the operator's steer routed by
		// the coordinator to its inbox, then produces a steered artifact and reports done.
		go func() {
			inbox := sx.ClientSubject(agentID)
			steerText := make(chan string, 1)
			sub, err := d.Subscribe(ctx, inbox, func(im sextant.Message) {
				if im.Frame.Author == d.ID() {
					return
				}
				if text, ok := workflow.ParseChatSteer(im.Frame.Record); ok {
					select {
					case steerText <- text:
					default:
					}
				}
			})
			if err != nil {
				return
			}
			defer sub.Stop()

			select {
			case text := <-steerText:
				// The steer ARRIVED. Act on it: write a distinct steered artifact whose body
				// quotes the operator's text (the behavioral change the AC demands), and
				// report the step done carrying it.
				name := "steered." + req.Job + "." + stepID
				body, _ := json.Marshal(map[string]string{"$type": "poem", "steered_by": text})
				if _, err := d.CreateArtifact(ctx, name, body); err != nil {
					return
				}
				ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "poem", Version: 1}}
				if steeredCh != nil {
					select {
					case steeredCh <- name:
					default:
					}
				}
				_ = d.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal())
			case <-time.After(8 * time.Second):
				// No steer arrived (the pre-fix silent-ignore). Report done with NO steered
				// artifact — the run cannot show behavioral influence, so the proof fails RED.
				ev.Artifacts = []workflow.ProducedArtifact{}
				_ = d.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal())
			case <-ctx.Done():
			}
		}()
	}, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("steerableDispatcher Subscribe: %v", err)
	}
}

// TestRun_OperatorSteerInfluencesActiveRun is the AC proof: an operator post on the run
// topic during an active work step demonstrably steers the run. It asserts (1) the worker
// acted on the operator's message (it produced the steered artifact, quoting the text),
// (2) the run's activity REFERENCES the operator's message, and (3) the steered artifact
// is attached to the run. On the pre-fix coordinator the steer reaches nothing — no
// steered artifact and no activity reference — so the test is RED.
func TestRun_OperatorSteerInfluencesActiveRun(t *testing.T) {
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	consumer := dialBusClient(t, b, "consumer")
	requester := dialBusClient(t, b, "requester")
	operator := dialBusClient(t, b, "operator")
	dispatcher := dialBusClient(t, b, "dispatcher")
	spawnSubj := "msg.topic.spawn"

	steeredCh := make(chan string, 1)
	steerableDispatcher(t, ctx, dispatcher, spawnSubj, steeredCh)
	startListenConsumer(t, ctx, consumer, spawnSubj, 20*time.Second)

	runID := "01STEER0000000000000000000A"
	run := workflow.Run{
		ID: runID, Status: workflow.RunRunning, Objective: "write a poem",
		Steps: []workflow.RunStep{
			{ID: "write", Label: "write the poem", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	// The operator posts a steer on the run topic WHILE the work step is active. The work
	// step is in flight as soon as the run starts (the worker waits for the steer), so the
	// run is live; poll until the work step is recorded running, then post.
	pollRun(t, ctx, requester, runID, 10*time.Second, func(r workflow.Run) bool {
		return len(r.Steps) > 0 && r.Steps[0].Status == workflow.StepRunning && r.Status == workflow.RunRunning
	})
	const steerText = "write the poem to its own artifact"
	if err := operator.Publish(ctx, workflow.RunTopicSubject(runID),
		chatMessage(steerText)); err != nil {
		t.Fatalf("operator publish steer: %v", err)
	}

	// The worker must have ACTED on the operator's message (it produced the steered
	// artifact) — the behavioral influence the AC demands, not mere dash display.
	var steeredName string
	select {
	case steeredName = <-steeredCh:
	case <-time.After(15 * time.Second):
		t.Fatalf("worker never acted on the operator steer — the steer reached no worker (the silent-ignore the fix must close)")
	}

	got := pollRun(t, ctx, requester, runID, 20*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Fatalf("final status = %q, want done; steps=%+v", got.Status, got.Steps)
	}

	// (1) The run's ACTIVITY references the operator's message — proof the steer reached
	// the run, recorded by the coordinator (co.applySteers), not a dead text box.
	if !activityReferences(got, steerText) {
		t.Fatalf("run activity does not reference the operator steer %q (the coordinator did not record the steer reaching the run)\n  activity=%+v", steerText, got.Activity)
	}

	// (2) The steered artifact (the behavioral change) is attached to the run and quotes
	// the operator's text.
	if !runHasArtifact(got, steeredName) {
		t.Fatalf("steered artifact %q not attached to the run; artifacts=%+v", steeredName, got.Artifacts)
	}
	art, err := requester.GetArtifact(ctx, steeredName)
	if err != nil {
		t.Fatalf("get steered artifact %q: %v", steeredName, err)
	}
	if !strings.Contains(string(art.Record), steerText) {
		t.Fatalf("steered artifact does not incorporate the operator's text.\n  want substring: %q\n  got: %s", steerText, string(art.Record))
	}
}

// TestRun_SteerAfterTerminalReportedNotApplied is the no-silent-ignore guard: a steer
// posted after the run is terminal is reported not-applied on the run topic, never
// silently dropped. It runs a fast run to done, then posts a steer and asserts the
// coordinator publishes a "not applied" notice back on the run topic.
func TestRun_SteerAfterTerminalReportedNotApplied(t *testing.T) {
	// Keep the run-topic subscription alive briefly past terminal so the coordinator can
	// answer a late steer (the production default is longer; a short grace keeps the test
	// fast while still proving the not-applied path).
	t.Cleanup(SetTerminalGraceHook(5 * time.Second))

	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	consumer := dialBusClient(t, b, "consumer")
	requester := dialBusClient(t, b, "requester")
	operator := dialBusClient(t, b, "operator")
	dispatcher := dialBusClient(t, b, "dispatcher")
	spawnSubj := "msg.topic.spawn"

	// A plain cooperating worker drives the run quickly to done (no steering needed here).
	cooperatingDispatcher(t, ctx, dispatcher, spawnSubj, 0)
	startListenConsumer(t, ctx, consumer, spawnSubj, 20*time.Second)

	runID := "01STEERTERM0000000000000AA"
	run := workflow.Run{
		ID: runID, Status: workflow.RunRunning, Objective: "quick run",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "do work", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	got := pollRun(t, ctx, requester, runID, 15*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Fatalf("final status = %q, want done", got.Status)
	}

	// Subscribe the run topic to catch the coordinator's not-applied notice, THEN post a
	// late steer. DeliverAll so we never race the notice.
	noticeCh := make(chan string, 4)
	_, err = operator.Subscribe(ctx, workflow.RunTopicSubject(runID), func(m sextant.Message) {
		if m.Frame.Author == operator.ID() {
			return // skip our own steer echo
		}
		if text, ok := workflow.ParseChatSteer(m.Frame.Record); ok {
			noticeCh <- text
		}
	}, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("subscribe run topic: %v", err)
	}

	const lateSteer = "change the ending"
	if err := operator.Publish(ctx, workflow.RunTopicSubject(runID), chatMessage(lateSteer)); err != nil {
		t.Fatalf("operator publish late steer: %v", err)
	}

	// The coordinator must publish a not-applied notice (never a silent drop) referencing
	// the run's terminal status.
	deadline := time.After(10 * time.Second)
	for {
		select {
		case text := <-noticeCh:
			if strings.Contains(text, "not applied") && strings.Contains(text, "done") {
				return // PASS: the late steer was reported not-applied
			}
			// keep waiting for the notice (ignore any unrelated chat frame)
		case <-deadline:
			t.Fatalf("a steer after the run went terminal was NOT reported not-applied on the run topic (silent drop — the bug this guards)")
		}
	}
}

// TestRun_SteerThreadsIntoNextStepPrompt proves the step-BOUNDARY leg: a steer that
// lands between steps (here, during step 1, gated so it is recorded before step 1
// returns) is threaded into the NEXT work step's prompt (steerHistory → workPrompt).
// This is the path the LIVE case leans on — a real worker often isn't mid-turn when the
// steer arrives, so same-step inbox delivery may miss and the steer must shape step N+1.
//
// The fake is built to ISOLATE the threading leg from the same-step inbox leg:
//   - step 1's worker waits for the coordinator-routed steer (so steerHistory is
//     populated before step 1 reports done, i.e. before step 2's workPrompt is built)
//     but does NOT incorporate it into step 1's output — it just gates done on receipt;
//   - step 2's worker reports the PROMPT it received back over promptCh.
//
// The test asserts step 2's prompt carries the steer text. RED if the steerHistory
// threading in workPrompt is removed (step 2's prompt would never carry the steer).
func TestRun_SteerThreadsIntoNextStepPrompt(t *testing.T) {
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	consumer := dialBusClient(t, b, "consumer")
	requester := dialBusClient(t, b, "requester")
	operator := dialBusClient(t, b, "operator")
	dispatcher := dialBusClient(t, b, "dispatcher")
	spawnSubj := "msg.topic.spawn"

	step1Running := make(chan struct{}, 1) // signals step 1's worker is dispatched + waiting
	step2Prompt := make(chan string, 1)    // step 2's worker reports the prompt it received

	_, err = dispatcher.Subscribe(ctx, spawnSubj, func(m sextant.Message) {
		var req struct {
			Type   string `json:"$type"`
			Job    string `json:"job,omitempty"`
			Prompt string `json:"prompt,omitempty"`
		}
		if err := json.Unmarshal(m.Frame.Record, &req); err != nil || req.Type != workflow.TypeSpawnRequest {
			return
		}
		agentID := "agent-" + m.Frame.ID[:8]
		ack := workflow.SpawnAck{Type: workflow.TypeSpawnAck, ID: agentID, RequestID: m.Frame.ID, Status: workflow.StatusOK}
		ackBytes, _ := json.Marshal(ack)
		if err := dispatcher.Publish(ctx, spawnSubj, json.RawMessage(ackBytes)); err != nil {
			return
		}
		stepID := parseDirective(req.Prompt, "RUN_STEP")
		ev := workflow.RunEvent{Step: stepID, Status: workflow.StepDone}

		switch {
		case strings.Contains(req.Prompt, "stopping brief"):
			ev.Outcome = workflow.RunDone
			name := "brief.stopping." + req.Job
			if _, err := dispatcher.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"brief","outcome":"done"}`)); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "stopping", Version: 1}}
			_ = dispatcher.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal())

		case stepID == "step1":
			// Gate step 1's done on the coordinator-routed steer landing on its inbox, so
			// the steer is in steerHistory BEFORE step 2's workPrompt is built. Step 1's
			// OUTPUT is unaffected (this isolates the threading leg from the same-step leg).
			go func() {
				inbox := sx.ClientSubject(agentID)
				got := make(chan struct{}, 1)
				sub, err := dispatcher.Subscribe(ctx, inbox, func(im sextant.Message) {
					if im.Frame.Author == dispatcher.ID() {
						return
					}
					if _, ok := workflow.ParseChatSteer(im.Frame.Record); ok {
						select {
						case got <- struct{}{}:
						default:
						}
					}
				})
				if err != nil {
					return
				}
				defer sub.Stop()
				select {
				case step1Running <- struct{}{}:
				default:
				}
				select {
				case <-got:
				case <-time.After(10 * time.Second):
				case <-ctx.Done():
					return
				}
				name := "deliverable." + req.Job + ".step1"
				if _, err := dispatcher.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"work","step":"step1"}`)); err != nil {
					return
				}
				ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "work", Version: 1}}
				_ = dispatcher.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal())
			}()

		default: // step2 — report the prompt it received, then complete.
			select {
			case step2Prompt <- req.Prompt:
			default:
			}
			name := "deliverable." + req.Job + ".step2"
			if _, err := dispatcher.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"work","step":"step2"}`)); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "work", Version: 1}}
			_ = dispatcher.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal())
		}
	}, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("subscribe threading dispatcher: %v", err)
	}
	startListenConsumer(t, ctx, consumer, spawnSubj, 20*time.Second)

	runID := "01STEERTHREAD000000000000A"
	run := workflow.Run{
		ID: runID, Status: workflow.RunRunning, Objective: "two steps; steer between them",
		Steps: []workflow.RunStep{
			{ID: "step1", Label: "first", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "step2", Label: "second", Kind: workflow.KindWork, Status: workflow.StepUpcoming},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")

	// Wait until step 1's worker is dispatched + waiting, then post the steer. It is
	// routed to step 1's worker (which then completes), so by the time step 2 is
	// dispatched the steer is in steerHistory and must appear in step 2's prompt.
	select {
	case <-step1Running:
	case <-time.After(15 * time.Second):
		t.Fatalf("step 1 worker never dispatched")
	}
	const steerText = "constrain the output to five lines"
	if err := operator.Publish(ctx, workflow.RunTopicSubject(runID), chatMessage(steerText)); err != nil {
		t.Fatalf("operator publish steer: %v", err)
	}

	select {
	case prompt := <-step2Prompt:
		if !strings.Contains(prompt, steerText) {
			t.Fatalf("step 2's prompt does NOT carry the between-steps steer (steerHistory threading missing).\n  want substring: %q\n  got prompt:\n%s", steerText, prompt)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("step 2 was never dispatched — the run did not advance past the steered step 1")
	}

	// And the run completes (the steer didn't wedge it).
	got := pollRun(t, ctx, requester, runID, 20*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Fatalf("final status = %q, want done; steps=%+v", got.Status, got.Steps)
	}
	// The steer was recorded on the run (never silently dropped), too.
	if !activityReferences(got, steerText) {
		t.Fatalf("run activity does not reference the steer %q; activity=%+v", steerText, got.Activity)
	}
}

// activityReferences reports whether any activity entry's text contains substr.
func activityReferences(r workflow.Run, substr string) bool {
	for _, a := range r.Activity {
		if strings.Contains(a.Text, substr) {
			return true
		}
	}
	return false
}

// runHasArtifact reports whether the run has an attached artifact with the given name.
func runHasArtifact(r workflow.Run, name string) bool {
	for _, a := range r.Artifacts {
		if a.Name == name {
			return true
		}
	}
	return false
}
