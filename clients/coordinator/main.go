// Command sextant-workflow is the reference run executor (TASK-236, ADR-0048).
//
// A run is one live instance of work, a CONVENTION over the two primitives, not a
// primitive of its own (ADR-0011): there is no engine in core. This coordinator is
// an ordinary bus client that runs the engine as a library and drives a
// sextant.workflow.run/v1 run to a terminal status:
//
//   - State is a sextant.workflow.run/v1 envelope held as an Artifact, keyed
//     RunStateName(id), SINGLE-WRITER (this coordinator) and CAS-checkpointed. The
//     dash writes it ONCE at spawn (its spawn act); the coordinator adopts it on a
//     run.start wake and owns it from there. A restarted coordinator re-reads it and
//     RESUMES at step granularity — idempotent for COMPLETED steps (it skips them). A
//     step left StepRunning by a crash is re-dispatched, which can double a dispatched
//     agent; crash-safe in-flight resume (record the agent/request id, re-attach
//     instead of re-spawn) is a known follow-up, not yet done.
//   - Steps run by kind: work → dispatch an agent (compose the M5.2 dispatcher);
//     checkpoint → pause for the operator (run.control approve/resume); brief → write
//     the terminal stopping brief, gated on a brief artifact (ADR-0048).
//   - Progress is the run's EMBEDDED activity stream (the dash polls the envelope).
//     A dispatched agent reports a step done with a run.event on
//     msg.workflow.run.<id>.events; cooperative control rides .control.
//
// A failed step drives the run to BLOCKED (there is no "failed" run status). Every
// blocking wait is deadline-bounded and logs on expiry (fail-loud, never a silent
// hang).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/love-lena/sextant/conventions/workflow/go"
	"github.com/love-lena/sextant/protocol/conninfo"
	"github.com/love-lena/sextant/protocol/sx"
	"github.com/love-lena/sextant/sdk/go"
)

func main() {
	fs := flag.NewFlagSet("sextant-workflow", flag.ExitOnError)
	creds := fs.String("creds", os.Getenv("SEXTANT_CREDS"), "coordinator credentials file (its own bus identity)")
	store := fs.String("store", os.Getenv("SEXTANT_STORE"), "bus store dir for bus.json discovery")
	url := fs.String("url", "", "bus URL (default: discovery file under --store)")
	id := fs.String("id", "", "run id to adopt directly (the dash already wrote the run artifact); empty = listen mode")
	spawnSubject := fs.String("spawn-subject", "msg.topic.spawn", "subject the M5.2 dispatcher watches for spawn.request")
	stepTimeout := fs.Duration("step-timeout", 90*time.Second, "max time for one step (spawn.ack + the agent's step-done) before it fails loud")
	agentMode := fs.Bool("agent-mode", false, "opt this run into the long-lived coordinator-AGENT review loop (TASK-242); default false = programmatic. The run envelope's agent_mode field also opts in.")
	_ = fs.Parse(os.Args[1:])

	if *creds == "" {
		fatal("usage: sextant-workflow --creds F --store DIR (--id ULID | <listen mode>) [--spawn-subject S] [--step-timeout D]")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	connCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c, err := sextant.Connect(connCtx, sextant.Options{
		CredsPath:    *creds,
		URL:          *url,
		ConnInfoPath: filepath.Join(*store, conninfo.DefaultFile),
		Logf:         func(string, ...any) {},
	})
	if err != nil {
		fatal("connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	// Listen mode: no --id — subscribe to run.start and adopt one run per request
	// (the dash's "spawn a run" path).
	if *id == "" {
		_, sub, err := newStartConsumer(ctx, c, *spawnSubject, *stepTimeout)
		if err != nil {
			fatal("%v", err)
		}
		defer sub.Stop()
		logf("coordinator up as %s; listening on %s for run.start", short(c.ID()), workflow.RunStartSubject)
		select {
		case <-ctx.Done():
			logf("signalled; shutting down")
		case <-c.Drained():
			logf("bus drained; shutting down")
		}
		return
	}

	co := newCoordinator(ctx, c, *spawnSubject, *stepTimeout)
	if err := co.adopt(ctx, *id); err != nil {
		fatal("%v", err)
	}
	// --agent-mode opts a directly-adopted run into the coordinator-agent review loop
	// (TASK-242), persisting the opt-in on the envelope so a resumed coordinator keeps it.
	// It only turns agent mode ON (never off) — a run the dash already marked agent_mode
	// stays in agent mode regardless of the flag.
	if *agentMode && !co.run.AgentMode && !workflow.IsTerminalRun(co.run.Status) {
		co.run.AgentMode = true
		if err := co.checkpoint(); err != nil {
			fatal("persist --agent-mode: %v", err)
		}
	}
	logf("coordinator up as %s; run %s (%d steps), status %s, agent-mode=%t", short(c.ID()), co.run.ID, len(co.run.Steps), co.run.Status, co.run.AgentMode)

	// Subscriptions: spawn.acks (to correlate dispatch steps), the run's event
	// stream (agents' step-done signals), and cooperative control. DeliverAll closes
	// the start race and lets a resumed coordinator see prior state.
	for _, s := range []struct {
		subj string
		h    sextant.Handler
	}{
		{*spawnSubject, co.onSpawn},
		{workflow.RunEventsSubject(co.run.ID), co.onEvent},
		{workflow.RunControlSubject(co.run.ID), co.onControl},
		{workflow.RunTopicSubject(co.run.ID), co.onSteer},
		{workflow.RunDecisionSubject(co.run.ID), co.onDecision}, // agent mode (no-op when off)
	} {
		sub, err := c.Subscribe(ctx, s.subj, s.h, sextant.DeliverAll())
		if err != nil {
			fatal("subscribe %s: %v", s.subj, err)
		}
		defer sub.Stop()
	}

	// Let DeliverAll replay any retained control settle before walking — a cancel
	// issued while this coordinator was down must be honoured on (re)start.
	co.settle()

	if err := co.walk(); err != nil {
		fatal("run %s: %v", co.run.ID, err)
	}
}

type coordinator struct {
	ctx          context.Context
	c            *sextant.Client
	spawnSubject string
	stepTimeout  time.Duration
	// terminalGrace is how long the run-topic subscription stays alive AFTER the run goes
	// terminal, so a steer arriving just-too-late is reported not-applied rather than
	// silently dropped by a coordinator already tearing down (TASK-246 no-silent-drop).
	// Past it the coordinator has released the run; a still-later post reaches no live
	// owner (that is "no coordinator", not a silent drop by a live one).
	terminalGrace time.Duration

	run workflow.Run
	rev uint64 // current revision of the state artifact (for CAS)

	// getArtifact is the coordinator's CONTENT-read seam — it returns an artifact's body
	// (defaults to the SDK client's GetArtifact). Routing every content read through one
	// named function lets a test observe exactly which artifacts the coordinator OPENS:
	// the content-opacity proof (AC#3) asserts the work/thread path reads NO step-output
	// artifact's content. The only content read is `adopt` reading the run-state envelope
	// (the coordinator's own single-writer artifact), never a worker's deliverable.
	getArtifact func(ctx context.Context, name string) (sextant.Artifact, error)

	// existsArtifact is the coordinator's EXISTENCE-PROBE seam — it confirms an artifact
	// is present on the bus and DISCARDS the body (defaults to a GetArtifact wrapper that
	// keeps only the error). It is deliberately DISTINCT from getArtifact: the proof gate
	// (TASK-243) existence-checks every step's reported artifacts through here without ever
	// inspecting their content, so the content-opacity proof (AC#3) can tell a content read
	// apart from an existence probe — probing existence is metadata, not a content read.
	existsArtifact func(ctx context.Context, name string) error

	// reviewerAgent is the long-lived coordinator-AGENT's id in agent mode (TASK-242),
	// empty in the default programmatic path. Stood up once on adopt; each completed step
	// is reviewed by DMing it and awaiting its run.decision. Guarded (set on the main
	// goroutine, read by onDecision/reviewStep).
	reviewerAgent string

	mu            sync.Mutex
	acks          map[string]workflow.SpawnAck    // spawn.request frame id -> ack
	doneEvents    map[string]workflow.RunEvent    // step id -> a non-self step-done event
	decisions     map[string]workflow.RunDecision // step id -> the agent's run.decision (agent mode)
	stepFeedback  map[string]string               // step id -> agent redo feedback, threaded into the re-dispatch prompt
	approved      bool
	cancelled     bool
	pendingSteers []steer       // operator steers to apply, drained on the main goroutine
	steerHistory  []string      // every applied steer's text, threaded into the next work step's prompt
	activeAgent   string        // the worker id of the step currently dispatched (for routing a steer)
	terminal      bool          // set in finish(): a steer arriving after this is reported not-applied
	ackCh         chan struct{} // wakes waiters: new ack
	evCh          chan struct{} // wakes waiters: new event
	ctlCh         chan struct{} // wakes waiters: control changed
	steerCh       chan struct{} // wakes waiters: an operator steer arrived on the run topic
	decCh         chan struct{} // wakes waiters: an agent run.decision arrived (agent mode)
}

// steer is one operator steering message off the run topic (msg.topic.run.<id>): the
// chat.message text and its bus-stamped author. The coordinator records it on the run
// and routes it to the active step's worker so it influences the run (TASK-246).
type steer struct {
	text string
	from string
}

func newCoordinator(ctx context.Context, c *sextant.Client, spawnSubject string, stepTimeout time.Duration) *coordinator {
	co := &coordinator{
		ctx: ctx, c: c, spawnSubject: spawnSubject, stepTimeout: stepTimeout,
		terminalGrace: 30 * time.Second,
		acks:          map[string]workflow.SpawnAck{}, doneEvents: map[string]workflow.RunEvent{},
		decisions: map[string]workflow.RunDecision{}, stepFeedback: map[string]string{},
		ackCh: make(chan struct{}, 1), evCh: make(chan struct{}, 1), ctlCh: make(chan struct{}, 1),
		steerCh: make(chan struct{}, 1), decCh: make(chan struct{}, 1),
	}
	co.getArtifact = c.GetArtifact
	co.existsArtifact = func(ctx context.Context, name string) error {
		_, err := c.GetArtifact(ctx, name) // existence probe: keep only the error, discard the body
		return err
	}
	if newCoordinatorHook != nil {
		newCoordinatorHook(co)
	}
	return co
}

// newCoordinatorHook, when set (tests only, via export_test.go), runs on every freshly
// built coordinator — the seam a test uses to observe its artifact reads. nil in prod.
var newCoordinatorHook func(*coordinator)

// adopt reads the run artifact the dash wrote (single-writer handoff: the dash
// created it at spawn; the coordinator owns it from here), (re)owns it, and resets a
// non-terminal run to running. Idempotent on resume: a terminal run is a no-op.
func (co *coordinator) adopt(ctx context.Context, runID string) error {
	art, err := co.getArtifact(ctx, workflow.RunStateName(runID))
	if err != nil {
		return fmt.Errorf("adopt %s: %w", runID, err)
	}
	r, ok := workflow.ParseRun(art.Record)
	if !ok {
		return fmt.Errorf("adopt %s: not a %s record", runID, workflow.KindRun)
	}
	co.run, co.rev = r, art.Revision
	co.run.Owner = co.c.ID()
	if workflow.IsTerminalRun(co.run.Status) {
		return nil
	}
	if len(co.run.Stop) == 0 {
		co.run.Stop = []string{"done — brief w/ proof of success", "blocked — brief documenting why"}
	}
	co.run.Status = workflow.RunRunning
	return co.checkpoint()
}

func (co *coordinator) nowMs() int64 { return time.Now().UnixMilli() }

// appendActivity adds an entry to the run's embedded activity stream (the dash's
// observability channel) and checkpoints. at is unix-ms to match the dash.
func (co *coordinator) appendActivity(glyph, text string) {
	co.run.Activity = append(co.run.Activity, workflow.ActivityEntry{
		ID:    fmt.Sprintf("a%d-%d", co.nowMs(), len(co.run.Activity)),
		Glyph: glyph, Text: text, Src: co.run.ID, At: co.nowMs(),
	})
	if err := co.checkpoint(); err != nil {
		logf("warn: checkpoint after activity %q: %v", text, err)
	}
}

// walk drives the steps: honour cancel, find the next not-done step, run it by kind,
// checkpoint + append activity. A resumed coordinator skips done steps; a resumed
// terminal run is a no-op. A failed step drives the run to BLOCKED (no failed status).
// (Named walk, not run, to avoid colliding with the `run` state field.)
func (co *coordinator) walk() error {
	if workflow.IsTerminalRun(co.run.Status) {
		logf("run %s already %s; nothing to do", co.run.ID, co.run.Status)
		return nil
	}
	// Agent mode (TASK-242): stand up the long-lived coordinator reviewer ONCE before the
	// first step. Idempotent on resume; a no-op (and zero traffic) in the default path.
	if err := co.standUpCoordinatorAgent(); err != nil {
		co.appendActivity("✗", fmt.Sprintf("agent mode: %v", err))
		return co.finish(workflow.RunBlocked, "coordinator agent stand-up failed")
	}
	for {
		if co.isCancelled() {
			return co.finish(workflow.RunCancelled, "cancelled by operator")
		}
		if co.ctx.Err() != nil {
			logf("signalled; leaving run %s resumable at its checkpoint", co.run.ID)
			return nil
		}
		idx := co.run.NextPending()
		if idx == -1 {
			return co.finish(workflow.RunDone, "all steps complete")
		}
		step := &co.run.Steps[idx]
		logf("step %s (%s): running", step.ID, step.Kind)
		term, err := co.runStep(idx)
		// A cancel can land mid-step (a checkpoint wait, or a bounded dispatch wait
		// woken by the cancel) — honour it as cancelled, never as a failed/blocked or
		// a spuriously-completed step.
		if co.isCancelled() {
			return co.finish(workflow.RunCancelled, "cancelled by operator")
		}
		if err != nil {
			step.Status = workflow.StepWaiting // surfaces as "needs you" next to a blocked run
			co.appendActivity("✗", fmt.Sprintf("step %q failed: %v", step.ID, err))
			return co.finish(workflow.RunBlocked, "step "+step.ID+" failed")
		}
		// Agent-mode review (TASK-242). The deterministic gates have ALREADY run inside
		// runStep (existence-checked refs), so the agent decision below sits ON the proof
		// floor and can never advance the run over an absent/fabricated deliverable (AC#7).
		// Reviewable kinds are work + brief (a checkpoint is itself an operator gate). The
		// review can override the brief's term (e.g. redo) or confirm it (advance).
		if co.agentEnabled() && reviewable(step.Kind) {
			act, rterm, err := co.reviewAndApply(idx, term)
			if co.isCancelled() {
				return co.finish(workflow.RunCancelled, "cancelled by operator")
			}
			if err != nil {
				step.Status = workflow.StepWaiting
				co.appendActivity("✗", fmt.Sprintf("agent review of step %q failed: %v", step.ID, err))
				return co.finish(workflow.RunBlocked, "agent review of step "+step.ID+" failed")
			}
			switch act {
			case actRedo:
				// redo-with-feedback: do NOT advance; loop re-runs the SAME step (its status
				// is reset to upcoming in reviewAndApply) with the feedback threaded in.
				continue
			case actStop:
				return co.finish(rterm, "agent stop")
			case actAdvance:
				term = rterm // honour any override (e.g. an advance over a brief's reported term)
			}
		}
		if term != "" {
			return co.finish(term, "stopping brief: "+term)
		}
		step.Status = workflow.StepDone
		if err := co.checkpoint(); err != nil {
			return err
		}
		co.appendActivity("✓", stepDoneText(step))
	}
}

// reviewable reports whether a step kind produces reviewable output in agent mode. Work and
// brief steps yield a deliverable the coordinator agent judges; a checkpoint is itself an
// operator gate, not something the agent re-reviews.
func reviewable(kind string) bool {
	return kind == workflow.KindWork || kind == workflow.KindBrief
}

// reviewAction is the outcome the shell applies after an agent decision.
type reviewAction int

const (
	actAdvance reviewAction = iota // proceed (advance or edit-then-advance)
	actRedo                        // re-dispatch the same step (status reset; feedback stored)
	actStop                        // stop the run now (terminal)
)

// reviewAndApply asks the resident coordinator agent to review the just-completed step and
// translates the FLAT-STEP-MODEL v1 decision into an action the walk loop applies. It is the
// SOLE writer of the run envelope on this path (single-writer, AC#3): the agent only emits a
// run.decision; the shell records the decision on the activity trail and any feedback, and
// resets the step on a redo. inTerm is the step's own terminal status (set by a brief step;
// "" for work). Returns (action, terminal-status-for-stop-or-advance, error).
func (co *coordinator) reviewAndApply(idx int, inTerm string) (reviewAction, string, error) {
	step := &co.run.Steps[idx]
	dec, err := co.reviewStep(step)
	if err != nil {
		return actAdvance, "", err
	}
	switch dec.Verb {
	case workflow.DecisionAdvance, workflow.DecisionEdit:
		// edit-then-advance: the agent already edited the DELIVERABLE artifact itself (its
		// own act, unbounded — AC#6); the shell just advances. Both verbs proceed; the brief
		// step's reported term (inTerm) is honoured.
		return actAdvance, inTerm, nil
	case workflow.DecisionRedo:
		// Re-dispatch the SAME step with the agent's feedback threaded into the next prompt
		// (AC#2). Reset the step to upcoming and clear its prior produced refs so the re-run
		// records fresh output; store the feedback keyed by step id.
		co.setStepFeedback(step.ID, dec.Feedback)
		step.Status = workflow.StepUpcoming
		step.Produced = nil
		if err := co.checkpoint(); err != nil {
			return actAdvance, "", err
		}
		co.appendActivity("↻", fmt.Sprintf("redo step %q with agent feedback", step.ID))
		return actRedo, "", nil
	case workflow.DecisionStop:
		// The agent halts the run now. The current step's gates already passed, so this is a
		// deliberate quality stop, not a failure → terminal done.
		return actStop, workflow.RunDone, nil
	default:
		// Unreachable: reviewStep already rejected any non-v1 verb. Defensive.
		return actAdvance, "", fmt.Errorf("unsupported decision verb %q", dec.Verb)
	}
}

func stepDoneText(s *workflow.RunStep) string {
	if s.Label != "" {
		return s.Label
	}
	return "step " + s.ID + " done"
}

// runStep runs one step by kind. A non-empty terminal return means the run should
// finish with that status (the brief step's outcome).
func (co *coordinator) runStep(idx int) (string, error) {
	step := &co.run.Steps[idx]
	step.Status = workflow.StepRunning
	if err := co.checkpoint(); err != nil {
		return "", err
	}
	switch step.Kind {
	case workflow.KindWork:
		return "", co.runDispatch(step, co.workPrompt(step))
	case workflow.KindCheckpoint:
		return "", co.runCheckpoint(step)
	case workflow.KindBrief:
		return co.runBrief(step)
	default:
		return "", fmt.Errorf("unknown step kind %q", step.Kind)
	}
}

// runDispatch composes the M5.2 dispatcher: publish a spawn.request, correlate the
// spawn.ack by requestId, then wait for the spawned agent to report the step done on
// the run's event stream. Each wait is bounded (fail-loud). It attaches any artifacts
// the agent reported in its done event.
func (co *coordinator) runDispatch(step *workflow.RunStep, prompt string) error {
	req := workflow.SpawnRequest{Prompt: prompt, Nickname: step.Label, Job: co.run.ID, Model: step.Model}
	out, err := co.c.PublishMsg(co.ctx, co.spawnSubject, req.Marshal())
	if err != nil {
		return fmt.Errorf("publish spawn.request: %w", err)
	}
	ack, ok := co.awaitAck(out.ID, co.stepTimeout)
	if !ok {
		return fmt.Errorf("no spawn.ack within %s", co.stepTimeout)
	}
	if ack.Status != workflow.StatusOK {
		return fmt.Errorf("dispatch rejected: %s", ack.Error)
	}
	step.Agent = ack.ID
	// This worker is now the live target for an operator steer: an operator post on the
	// run topic is routed to its inbox so it can incorporate it mid-step (TASK-246). The
	// worker is resident for the duration of its step (a pi RPC session), so a frame to
	// its inbox lands as a follow-up turn (drained at agent_end before it winds down).
	co.setActiveAgent(ack.ID)
	defer co.setActiveAgent("")
	ev, ok := co.awaitStepDone(step.ID, co.stepTimeout)
	if !ok {
		return fmt.Errorf("agent %s never reported step %q done within %s", short(ack.ID), step.ID, co.stepTimeout)
	}
	// AC#2 — a work step's deliverable must be a durable artifact. A step that reports
	// done but attaches no artifact is the 01KW8J2N hollow case (its output lived only
	// in agent.activity and was lost); that is a step FAILURE → the run blocks, never a
	// silent advance to done. The coordinator keys this on the step boundary, not on the
	// artifact's kind/name (a worker is a model; its label is arbitrary).
	if len(ev.Artifacts) == 0 {
		return fmt.Errorf("work step %q reported done but produced no artifact (output not captured; AC#2 gate)", step.ID)
	}
	// EXISTENCE gate (TASK-243): the count gate above only proves the worker NAMED an
	// artifact — not that it created one. Independently confirm every reported ref exists
	// on the bus before recording/threading it, so a work step cannot certify done against
	// a phantom deliverable (the 01KW8J2N fabrication class, relocatable to ANY step — not
	// only the brief). The brief step (runBrief) applies the same check; both gates required.
	if err := co.verifyReportedArtifactsExist(ev.Artifacts); err != nil {
		return err
	}
	// Record the refs (name/kind/version only) on the step — the per-step deliverable
	// the next step pipes against (AC#1) and the distinct-artifact-per-step ledger
	// (AC#2). Refs only: never the content (AC#3).
	step.Produced = append(step.Produced, ev.Artifacts...)
	co.attachArtifacts(ev.Artifacts)
	return nil
}

// workPrompt augments a work step's task with (a) REFERENCES to the artifacts prior
// steps produced — so step N operates on step N-1's real deliverable instead of redoing
// it from scratch (AC#1, the 01KW8J2N piping bug) — and (b) the reporting directive the
// agent uses to signal completion on the run's event stream.
//
// Content-opacity (AC#3): the coordinator threads only the artifact NAMES it already
// holds in run state (from each step's run.event). It does NOT read the upstream
// artifacts' content to summarise or inline them — the downstream worker dereferences
// the named artifact itself (sextant_artifact_get). The coordinator issues no
// artifact_get on this path.
func (co *coordinator) workPrompt(step *workflow.RunStep) string {
	prompt := fmt.Sprintf("%s\n\n%s", co.run.Objective, step.Label)
	if inputs := co.upstreamArtifacts(step); len(inputs) > 0 {
		prompt += "\n\nINPUT ARTIFACTS (produced by prior steps of this run — fetch each with sextant_artifact_get and build on its content; do NOT start from scratch):"
		for _, a := range inputs {
			prompt += "\n- " + artifactRef(a)
		}
	}
	// Thread any operator steers applied so far into the step's brief, so a steer that
	// landed between steps (no worker resident to DM at the time) still shapes the NEXT
	// step's work — the step-boundary half of live steering (TASK-246).
	if steers := co.steerHistorySnapshot(); len(steers) > 0 {
		prompt += "\n\nOPERATOR STEERING (incorporate these directions from the operator into this run):"
		for _, s := range steers {
			prompt += "\n- " + s
		}
	}
	// Agent mode (TASK-242): if the coordinator agent returned redo-with-feedback for this
	// step, thread its feedback into the re-dispatch so the SAME step re-runs WITH the
	// guidance (AC#2). Present only on a redo loop; absent on a first dispatch.
	if fb := co.stepFeedbackFor(step.ID); fb != "" {
		prompt += "\n\nCOORDINATOR FEEDBACK (your prior attempt was sent back for rework — address this):\n- " + fb
	}
	prompt += fmt.Sprintf("\nRUN_EVENTS=%s RUN_STEP=%s",
		workflow.RunEventsSubject(co.run.ID), step.ID)
	return prompt
}

// upstreamArtifacts gathers the artifact refs produced by the work steps that ran BEFORE
// step (in step order, up to but excluding it), de-duped by name with the latest-seen
// ref winning. Refs only — the coordinator never opens the artifacts (AC#3).
func (co *coordinator) upstreamArtifacts(step *workflow.RunStep) []workflow.ProducedArtifact {
	var out []workflow.ProducedArtifact
	at := map[string]int{} // name -> index in out
	for i := range co.run.Steps {
		s := &co.run.Steps[i]
		if s.ID == step.ID {
			break // only steps strictly before this one feed it
		}
		if s.Kind != workflow.KindWork {
			continue
		}
		for _, a := range s.Produced {
			if j, seen := at[a.Name]; seen {
				out[j] = a
				continue
			}
			at[a.Name] = len(out)
			out = append(out, a)
		}
	}
	return out
}

// artifactRef renders an artifact reference (name + optional kind/version) — metadata
// only, never content. The single source for how a ref appears in a worker prompt.
func artifactRef(a workflow.ProducedArtifact) string {
	ref := a.Name
	if a.Kind != "" {
		ref += " (kind " + a.Kind
		if a.Version > 0 {
			ref += fmt.Sprintf(", v%d", a.Version)
		}
		ref += ")"
	} else if a.Version > 0 {
		ref += fmt.Sprintf(" (v%d)", a.Version)
	}
	return ref
}

// attachArtifacts records artifacts an agent reported in its done event onto the run
// (ADR-0048), de-duped by name, newest version wins.
func (co *coordinator) attachArtifacts(arts []workflow.ProducedArtifact) {
	for _, a := range arts {
		replaced := false
		for i := range co.run.Artifacts {
			if co.run.Artifacts[i].Name == a.Name {
				co.run.Artifacts[i] = a
				replaced = true
				break
			}
		}
		if !replaced {
			co.run.Artifacts = append(co.run.Artifacts, a)
		}
	}
	if len(arts) > 0 {
		if err := co.checkpoint(); err != nil {
			logf("warn: checkpoint after attaching artifacts: %v", err)
		}
	}
}

// setActiveAgent records (or clears) the worker id of the step currently dispatched,
// the target an operator steer is routed to. Guarded because onSteer reads it on a
// delivery goroutine while runDispatch sets it on the main goroutine.
func (co *coordinator) setActiveAgent(id string) {
	co.mu.Lock()
	co.activeAgent = id
	co.mu.Unlock()
}

// applySteers drains the operator steers that arrived since the last call and applies
// each to the LIVE run (TASK-246). Run only on the main goroutine (it mutates co.run
// via appendActivity, preserving single-writer). For each steer it:
//   - records an activity entry that REFERENCES the operator's message (so the run's
//     embedded stream — what the dash shows — proves the steer reached the run, not a
//     dead text box), and
//   - routes the steer to the active step's worker by publishing a chat.message to the
//     worker's inbox (msg.client.<agent>). The worker is resident for its step, so this
//     lands as a follow-up turn it incorporates mid-step; if no step is in flight right
//     now, the steer still rides steerHistory into the NEXT work step's prompt (applied
//     at the step boundary). Either way it influences the active run.
//
// A steer is never silently dropped: a steer arriving after the run is terminal is
// handled in onSteer (a not-applied notice), so it never reaches this queue.
func (co *coordinator) applySteers() {
	co.mu.Lock()
	pending := co.pendingSteers
	co.pendingSteers = nil
	agent := co.activeAgent
	co.mu.Unlock()
	for _, s := range pending {
		co.mu.Lock()
		co.steerHistory = append(co.steerHistory, s.text)
		co.mu.Unlock()
		co.appendActivity("✎", fmt.Sprintf("operator steer from %s: %q", short(s.from), s.text))
		if agent == "" {
			logf("steer queued for the next step (no step in flight): %q", s.text)
			continue
		}
		notice := chatMessage(fmt.Sprintf("OPERATOR STEER for this run (incorporate it into your current task): %s", s.text))
		if err := co.c.Publish(co.ctx, sx.ClientSubject(agent), notice); err != nil {
			logf("route steer to worker %s: %v", short(agent), err)
			continue
		}
		logf("routed steer to active worker %s: %q", short(agent), s.text)
	}
}

// steerHistorySnapshot returns a copy of the steers applied so far (for threading into
// a work step's prompt). Guarded: onSteer/applySteers touch the slice off other paths.
func (co *coordinator) steerHistorySnapshot() []string {
	co.mu.Lock()
	defer co.mu.Unlock()
	out := make([]string, len(co.steerHistory))
	copy(out, co.steerHistory)
	return out
}

// runCheckpoint sets the run to waiting and blocks until the operator approves
// (run.control approve/resume) or cancels. Cooperative: the coordinator only acts on
// the verb the operator sends (ADR-0048). (TASK-225)
func (co *coordinator) runCheckpoint(step *workflow.RunStep) error {
	step.Status = workflow.StepWaiting
	co.run.Status = workflow.RunWaiting
	if err := co.checkpoint(); err != nil {
		return err
	}
	co.appendActivity("❡", "awaiting operator: "+stepDoneText(step))
	// Do NOT reset co.approved at entry: an approve published just before a restart is
	// replayed by DeliverAll and must still take. We consume it (reset to false) only
	// when we act on it, so the NEXT checkpoint still waits for its own approve.
	for {
		// An operator can steer while the run is paused for review; record it now and
		// thread it into the next step (no worker is in flight at a checkpoint, so it
		// applies at the next work step's boundary — never silently dropped). (TASK-246)
		co.applySteers()
		co.mu.Lock()
		approved, cancelled := co.approved, co.cancelled
		if approved {
			co.approved = false // consume this approve so the next checkpoint waits
		}
		co.mu.Unlock()
		if cancelled {
			return nil // run() sees isCancelled() and finishes cancelled
		}
		if approved {
			step.Status = workflow.StepDone
			co.run.Status = workflow.RunRunning
			if err := co.checkpoint(); err != nil {
				return err
			}
			co.appendActivity("✓", "operator approved: "+stepDoneText(step))
			return nil
		}
		select {
		case <-co.ctlCh:
		case <-co.steerCh: // an operator steer arrived during the checkpoint
		case <-co.ctx.Done():
			return nil // resumable: re-adopt re-enters the still-waiting checkpoint
		}
	}
}

// runBrief dispatches an agent prompted with the run's stop conditions to write the
// terminal stopping brief, then GATES: the run may not go terminal without a brief
// artifact attached (ADR-0048 "never halt without posting the brief"). The agent's
// reported outcome (done|blocked) becomes the terminal run status. (AC #4)
func (co *coordinator) runBrief(step *workflow.RunStep) (string, error) {
	req := workflow.SpawnRequest{Prompt: co.briefPrompt(step), Nickname: step.Label, Job: co.run.ID, Model: step.Model}
	out, err := co.c.PublishMsg(co.ctx, co.spawnSubject, req.Marshal())
	if err != nil {
		return "", fmt.Errorf("publish brief spawn.request: %w", err)
	}
	ack, ok := co.awaitAck(out.ID, co.stepTimeout)
	if !ok {
		return "", fmt.Errorf("no spawn.ack for brief within %s", co.stepTimeout)
	}
	if ack.Status != workflow.StatusOK {
		return "", fmt.Errorf("brief dispatch rejected: %s", ack.Error)
	}
	step.Agent = ack.ID
	ev, ok := co.awaitStepDone(step.ID, co.stepTimeout)
	if !ok {
		return "", fmt.Errorf("brief agent never reported done within %s", co.stepTimeout)
	}
	co.attachArtifacts(ev.Artifacts)
	// Stop gate, two parts:
	// (1) The brief step must have PRODUCED an artifact. Keyed on the step boundary,
	//     NOT the artifact's kind/name — a worker is a model and its kind label is
	//     arbitrary (observed live: "brief", "brief.stopping", "stopping").
	if len(ev.Artifacts) == 0 {
		return "", fmt.Errorf("brief step reported done but produced no artifact (stop gate)")
	}
	// (2) Every artifact the brief step's worker REPORTED producing must actually EXIST
	//     on the bus. The coordinator confirms the deliverables INDEPENDENTLY (it is
	//     deterministic code, not the AI worker) instead of trusting the worker's
	//     say-so, so a run cannot reach `done` on a fabricated proof (TASK-243: a brief
	//     certified done against a poem artifact that never existed). The SAME existence
	//     check runs on every work step (runDispatch) — a phantom blocks at any step.
	if err := co.verifyReportedArtifactsExist(ev.Artifacts); err != nil {
		return "", err
	}
	step.Status = workflow.StepDone
	if err := co.checkpoint(); err != nil {
		return "", err
	}
	if ev.Outcome == workflow.RunBlocked {
		return workflow.RunBlocked, nil
	}
	return workflow.RunDone, nil // default success — a posted brief with no explicit blocked
}

// verifyReportedArtifactsExist is the independent, shape-independent stop-gate check
// applied to EVERY step: for each artifact a step's worker REPORTED producing in its
// run.event (the typed Artifacts metadata — collected mechanically by the worker's
// runtime, not parsed from any content), the coordinator fetches it from the bus and
// confirms it EXISTS. A missing one means the worker certified the step done against an
// artifact it did not actually produce — the run blocks (TASK-243). The coordinator is a
// deterministic process separate from the AI worker, so this is genuine external
// verification, not the system self-reporting success.
//
// It decides SOLELY from this typed metadata and never opens or parses any artifact's
// content (AC2/AC4): the gate carries no notion of which brief-body keys name proof, so
// it cannot be evaded by a brief that declares its deliverable under a novel key — there
// is no key set to drift from. Whether a brief's prose accurately describes its
// deliverable is content, judged by the opt-in agent-mode reviewer (TASK-242), not here.
//
// Applied to BOTH work steps (runDispatch) and the brief step (runBrief): a phantom
// reported ref blocks at ANY step, not only the brief. The hollow-step COUNT gate
// (len==0) and this EXISTENCE gate are distinct and both required — a worker that
// reports a nonexistent name passes the count but fails existence.
func (co *coordinator) verifyReportedArtifactsExist(arts []workflow.ProducedArtifact) error {
	for _, a := range arts {
		if err := co.existsArtifact(co.ctx, a.Name); err != nil {
			return fmt.Errorf("worker reported producing artifact %q but it does not exist on the bus (fabricated proof, stop gate): %w", a.Name, err)
		}
	}
	return nil
}

// briefPrompt hands the agent the run's stop conditions so it writes a brief that
// justifies the stop, plus the reporting directive.
func (co *coordinator) briefPrompt(step *workflow.RunStep) string {
	prompt := fmt.Sprintf(
		"Write the stopping brief for this run as an artifact of kind \"brief\".\nObjective: %s\nStop when ANY of these is met (pick the one that holds and justify it):\n- %s\n\nReport done with the brief artifact in `artifacts` and `outcome` = done or blocked.\n"+
			"PROOF MUST BE REAL: any deliverable you cite as proof of completion MUST be a durable artifact you actually CREATED (via sextant_artifact_put) — not text that lives only inside this brief. The run is GATED on the artifacts you report producing: each one is existence-checked on the bus, and any that does not exist will BLOCK the run. Never report producing an artifact you did not create.\nRUN_EVENTS=%s RUN_STEP=%s",
		co.run.Objective, joinStops(co.run.Stop),
		workflow.RunEventsSubject(co.run.ID), step.ID,
	)
	// Agent-mode redo feedback threads into the brief re-dispatch too (AC#2): the same brief
	// step re-runs with the coordinator agent's guidance.
	if fb := co.stepFeedbackFor(step.ID); fb != "" {
		prompt += "\n\nCOORDINATOR FEEDBACK (your prior brief was sent back for rework — address this):\n- " + fb
	}
	return prompt
}

func joinStops(stops []string) string {
	if len(stops) == 0 {
		return "done — brief w/ proof of success\n- blocked — brief documenting why"
	}
	out := stops[0]
	for _, s := range stops[1:] {
		out += "\n- " + s
	}
	return out
}

// finish records a terminal run status, checkpoints, and appends the closing activity.
func (co *coordinator) finish(status, note string) error {
	co.run.Status = status
	// Mark terminal under the lock BEFORE checkpointing, so a steer racing in on the
	// delivery goroutine sees the run is closed and is reported not-applied rather than
	// enqueued for a walk that has already returned (TASK-246 no-silent-drop guard).
	co.mu.Lock()
	co.terminal = true
	co.mu.Unlock()
	if err := co.checkpoint(); err != nil {
		return err
	}
	text := "run " + status
	if note != "" {
		text += ": " + note
	}
	co.appendActivity(terminalGlyph(status), text)
	logf("run %s: %s", co.run.ID, status)
	return nil
}

func terminalGlyph(status string) string {
	switch status {
	case workflow.RunDone:
		return "✓"
	case workflow.RunCancelled:
		return "⊘"
	default:
		return "✗"
	}
}

// checkpoint persists the current state envelope with CAS on the tracked revision.
// Single-writer by convention, so a conflict is rare; on one we re-read and retry.
func (co *coordinator) checkpoint() error {
	for attempt := 0; attempt < 5; attempt++ {
		rev, err := co.c.UpdateArtifact(co.ctx, workflow.RunStateName(co.run.ID), co.run.Marshal(), co.rev)
		if err == nil {
			co.rev = rev
			return nil
		}
		art, gerr := co.c.GetArtifact(co.ctx, workflow.RunStateName(co.run.ID))
		if gerr != nil {
			return fmt.Errorf("checkpoint %s: %w", co.run.ID, err)
		}
		co.rev = art.Revision
	}
	return fmt.Errorf("checkpoint %s: exhausted CAS retries", co.run.ID)
}

// --- subscription handlers (run on the SDK's delivery goroutines) ---

func (co *coordinator) onSpawn(m sextant.Message) {
	a, ok := workflow.ParseSpawnAck(m.Frame.Record)
	if !ok {
		return
	}
	co.mu.Lock()
	co.acks[a.RequestID] = a
	co.mu.Unlock()
	wake(co.ackCh)
}

func (co *coordinator) onEvent(m sextant.Message) {
	if m.Frame.Author == co.c.ID() {
		return
	}
	ev, ok := workflow.ParseRunEvent(m.Frame.Record)
	if !ok || ev.Step == "" || ev.Status != workflow.StepDone {
		return
	}
	co.mu.Lock()
	co.doneEvents[ev.Step] = ev
	co.mu.Unlock()
	wake(co.evCh)
}

// onSteer handles an operator steer on the run topic (msg.topic.run.<id>) — the
// human-facing channel the dash run view posts to (TASK-246). It acts only on a real
// chat.message steer from someone OTHER than this coordinator (never its own echoed
// not-applied notice). A steer that arrives while the run is live is enqueued and the
// main goroutine drains it (records it on the run + routes it to the active worker);
// a steer that arrives once the run is TERMINAL cannot influence it, so instead of
// silently dropping it (the 01KW8J2N bug this fixes) the coordinator publishes a
// not-applied notice back on the same topic so the operator's thread shows the outcome.
//
// It runs on a delivery goroutine, so it only touches mutex-guarded state and publishes
// (thread-safe via the SDK client); it never mutates co.run directly — that is the main
// goroutine's job, preserving the single-writer discipline on the run envelope.
func (co *coordinator) onSteer(m sextant.Message) {
	if m.Frame.Author == co.c.ID() {
		return
	}
	text, ok := workflow.ParseChatSteer(m.Frame.Record)
	if !ok {
		return
	}
	co.mu.Lock()
	terminal := co.terminal
	if !terminal {
		co.pendingSteers = append(co.pendingSteers, steer{text: text, from: m.Frame.Author})
	}
	co.mu.Unlock()
	if terminal {
		// Never a silent drop: tell the operator the steer landed too late to apply.
		notice := chatMessage(fmt.Sprintf("steer not applied — run %s is already %s: %q", short(co.run.ID), co.run.Status, text))
		if err := co.c.Publish(context.Background(), workflow.RunTopicSubject(co.run.ID), notice); err != nil {
			logf("publish steer-not-applied notice: %v", err)
		}
		logf("steer from %s arrived after run %s went %s: not applied", short(m.Frame.Author), short(co.run.ID), co.run.Status)
		return
	}
	logf("steer from %s on run %s: %q", short(m.Frame.Author), short(co.run.ID), text)
	wake(co.steerCh)
}

// chatMessage renders a plain {$type:chat.message,text} record — the opaque chat
// convention the run topic carries (the coordinator's not-applied notice rides it so
// the dash thread renders it like any other post).
func chatMessage(text string) json.RawMessage {
	b, _ := json.Marshal(struct {
		Type string `json:"$type"`
		Text string `json:"text"`
	}{Type: workflow.TypeChatMessage, Text: text})
	return b
}

func (co *coordinator) onControl(m sextant.Message) {
	ctl, ok := workflow.ParseRunControl(m.Frame.Record)
	if !ok {
		return
	}
	co.mu.Lock()
	switch ctl.Verb {
	case workflow.CtlPause:
		// pause is reflected only inside a checkpoint wait; a bare pause between
		// steps is a no-op in the flat model (a checkpoint step is the pause point).
	case workflow.CtlResume, workflow.CtlApprove:
		co.approved = true
	case workflow.CtlCancel:
		co.cancelled = true
	}
	co.mu.Unlock()
	logf("control: %s", ctl.Verb)
	wake(co.ctlCh)
}

// --- bounded waits ---

func (co *coordinator) awaitAck(requestID string, timeout time.Duration) (workflow.SpawnAck, bool) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	for {
		if co.isCancelled() {
			return workflow.SpawnAck{}, false // cancelled mid-step → abort; walk() finishes cancelled
		}
		co.mu.Lock()
		a, ok := co.acks[requestID]
		if ok {
			delete(co.acks, requestID) // matched; don't let the map grow unbounded
		}
		co.mu.Unlock()
		if ok {
			return a, true
		}
		select {
		case <-co.ackCh:
		case <-co.ctlCh: // a cancel must abort a step wait promptly, not after step-timeout
		case <-t.C:
			return workflow.SpawnAck{}, false
		case <-co.ctx.Done():
			return workflow.SpawnAck{}, false
		}
	}
}

func (co *coordinator) awaitStepDone(stepID string, timeout time.Duration) (workflow.RunEvent, bool) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	for {
		if co.isCancelled() {
			return workflow.RunEvent{}, false // cancelled mid-step → abort; walk() finishes cancelled
		}
		// Apply any operator steer that arrived while this step is in flight, BEFORE
		// checking for done: a steer mid-step is routed to the live worker so it can
		// act on it within this same step (TASK-246).
		co.applySteers()
		co.mu.Lock()
		ev, ok := co.doneEvents[stepID]
		co.mu.Unlock()
		if ok {
			return ev, true
		}
		select {
		case <-co.evCh:
		case <-co.steerCh: // an operator steer arrived — route it to the live worker
		case <-co.ctlCh: // a cancel must abort a step wait promptly, not after step-timeout
		case <-t.C:
			return workflow.RunEvent{}, false
		case <-co.ctx.Done():
			return workflow.RunEvent{}, false
		}
	}
}

func (co *coordinator) isCancelled() bool {
	co.mu.Lock()
	defer co.mu.Unlock()
	return co.cancelled
}

// settle gives DeliverAll a moment to replay retained control before the walk begins,
// so a cancel issued while the coordinator was down is honoured rather than raced past
// into the first step. A local-bus heuristic, not a guaranteed barrier.
func (co *coordinator) settle() {
	select {
	case <-time.After(300 * time.Millisecond):
	case <-co.ctx.Done():
	}
}

// wake does a non-blocking send on a 1-buffered signal channel.
func wake(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// --- run.start consumer ---

// startConsumer subscribes to msg.topic.run.start, dedups by frame ID (in-memory),
// and adopts exactly one run per fresh request. It acks every handled frame — success
// or failure — so requesters are never left waiting on silence (fail-loud).
type startConsumer struct {
	ctx          context.Context
	c            *sextant.Client
	spawnSubject string
	stepTimeout  time.Duration

	mu   sync.Mutex
	seen map[string]bool // request frame ids already handled (dedup a redelivery on reconnect)
}

// newStartConsumer builds and subscribes a startConsumer on workflow.RunStartSubject.
func newStartConsumer(ctx context.Context, c *sextant.Client, spawnSubject string, stepTimeout time.Duration) (*startConsumer, sextant.Subscription, error) {
	sc := &startConsumer{
		ctx: ctx, c: c, spawnSubject: spawnSubject, stepTimeout: stepTimeout,
		seen: map[string]bool{},
	}
	// New-only delivery (NOT DeliverAll): a run.start is a LIVE command, not a durable
	// queue to replay. DeliverAll re-delivered every historical start on each (re)start
	// — including stale ones whose step can never complete — so a restarted listen-mode
	// coordinator re-ran them, timed out, and crash-looped (TASK-192). A start published
	// while the coordinator is briefly down is intentionally missed (the requester
	// re-issues); the seen fence still dedups a redelivery on reconnect.
	sub, err := c.Subscribe(ctx, workflow.RunStartSubject, sc.handle)
	if err != nil {
		return nil, nil, fmt.Errorf("subscribe %s: %w", workflow.RunStartSubject, err)
	}
	return sc, sub, nil
}

// handle processes one frame on RunStartSubject. It acts only on run.start records
// (ignoring its own echoed run.start.acks and anything else), dedups by request frame
// id, adopts the run the dash wrote, ACKS (ok + id/nonce), then runs the coordinator
// in a goroutine so the handler does not block the delivery loop.
func (sc *startConsumer) handle(m sextant.Message) {
	req, ok := workflow.ParseRunStartRequest(m.Frame.Record)
	if !ok {
		return
	}
	reqID := m.Frame.ID

	sc.mu.Lock()
	if sc.seen[reqID] {
		sc.mu.Unlock()
		return
	}
	sc.seen[reqID] = true
	sc.mu.Unlock()

	logf("run.start %s from %s: run=%s", short(reqID), short(m.Frame.Author), short(req.ID))

	co, stopSubs, err := sc.prepareRun(req.ID)
	ack := workflow.RunStartAck{ID: req.ID, Nonce: req.Nonce, RequestID: reqID}
	if err != nil {
		ack.Status = workflow.StatusError
		ack.Error = err.Error()
		logf("run.start %s: prepare failed: %v", short(reqID), err)
	} else {
		ack.Status = workflow.StatusOK
	}
	if perr := sc.c.Publish(context.Background(), workflow.RunStartSubject, ack.Marshal()); perr != nil {
		logf("publish run.start.ack: %v", perr)
	}
	if co == nil {
		return
	}
	go func() {
		defer stopSubs()
		if err := co.walk(); err != nil {
			logf("run %s error: %v", short(req.ID), err)
		}
		// Keep the run-topic subscription alive for a grace window past terminal so a
		// steer arriving just-too-late is reported not-applied (onSteer), never silently
		// dropped by a coordinator already tearing down (TASK-246). Cut short on shutdown.
		co.holdForLateSteers()
	}()
}

// holdForLateSteers keeps the coordinator's subscriptions (incl. the run topic) alive for
// terminalGrace after the run goes terminal, so onSteer can answer a late steer with a
// not-applied notice instead of the silent drop that teardown would cause. Returns at once
// if the grace is zero or the context is already done.
func (co *coordinator) holdForLateSteers() {
	if co.terminalGrace <= 0 {
		return
	}
	t := time.NewTimer(co.terminalGrace)
	defer t.Stop()
	select {
	case <-t.C:
	case <-co.ctx.Done():
	}
}

// prepareRun builds the coordinator, subscribes its helper subjects (spawn, the run's
// events + control), and adopts the run the dash wrote. It returns the coordinator
// (ready to run), a stopSubs closure, and any error. On failure: (nil, noop, err).
func (sc *startConsumer) prepareRun(runID string) (*coordinator, func(), error) {
	co := newCoordinator(sc.ctx, sc.c, sc.spawnSubject, sc.stepTimeout)

	var subs []sextant.Subscription
	for _, s := range []struct {
		subj string
		h    sextant.Handler
	}{
		{sc.spawnSubject, co.onSpawn},
		{workflow.RunEventsSubject(runID), co.onEvent},
		{workflow.RunControlSubject(runID), co.onControl},
		{workflow.RunTopicSubject(runID), co.onSteer},
		{workflow.RunDecisionSubject(runID), co.onDecision}, // agent mode (no-op when off)
	} {
		sub, err := sc.c.Subscribe(sc.ctx, s.subj, s.h, sextant.DeliverAll())
		if err != nil {
			for _, s := range subs {
				s.Stop()
			}
			return nil, func() {}, fmt.Errorf("subscribe %s: %w", s.subj, err)
		}
		subs = append(subs, sub)
	}
	stopSubs := func() {
		for _, s := range subs {
			s.Stop()
		}
	}

	if err := co.adopt(sc.ctx, runID); err != nil {
		stopSubs()
		return nil, func() {}, err
	}
	co.settle()
	return co, stopSubs, nil
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}

func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "sextant-workflow: "+format+"\n", a...)
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "sextant-workflow: "+format+"\n", a...)
	os.Exit(1)
}
