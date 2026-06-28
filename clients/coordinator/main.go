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
	logf("coordinator up as %s; run %s (%d steps), status %s", short(c.ID()), co.run.ID, len(co.run.Steps), co.run.Status)

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

	run workflow.Run
	rev uint64 // current revision of the state artifact (for CAS)

	mu         sync.Mutex
	acks       map[string]workflow.SpawnAck // spawn.request frame id -> ack
	doneEvents map[string]workflow.RunEvent // step id -> a non-self step-done event
	approved   bool
	cancelled  bool
	ackCh      chan struct{} // wakes waiters: new ack
	evCh       chan struct{} // wakes waiters: new event
	ctlCh      chan struct{} // wakes waiters: control changed
}

func newCoordinator(ctx context.Context, c *sextant.Client, spawnSubject string, stepTimeout time.Duration) *coordinator {
	return &coordinator{
		ctx: ctx, c: c, spawnSubject: spawnSubject, stepTimeout: stepTimeout,
		acks: map[string]workflow.SpawnAck{}, doneEvents: map[string]workflow.RunEvent{},
		ackCh: make(chan struct{}, 1), evCh: make(chan struct{}, 1), ctlCh: make(chan struct{}, 1),
	}
}

// adopt reads the run artifact the dash wrote (single-writer handoff: the dash
// created it at spawn; the coordinator owns it from here), (re)owns it, and resets a
// non-terminal run to running. Idempotent on resume: a terminal run is a no-op.
func (co *coordinator) adopt(ctx context.Context, runID string) error {
	art, err := co.c.GetArtifact(ctx, workflow.RunStateName(runID))
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
	req := workflow.SpawnRequest{Prompt: prompt, Nickname: step.Label, Job: co.run.ID}
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
	ev, ok := co.awaitStepDone(step.ID, co.stepTimeout)
	if !ok {
		return fmt.Errorf("agent %s never reported step %q done within %s", short(ack.ID), step.ID, co.stepTimeout)
	}
	co.attachArtifacts(ev.Artifacts)
	return nil
}

// workPrompt augments a work step's task with the reporting directive the agent uses
// to signal completion on the run's event stream.
func (co *coordinator) workPrompt(step *workflow.RunStep) string {
	return fmt.Sprintf("%s\n\n%s\nRUN_EVENTS=%s RUN_STEP=%s",
		co.run.Objective, step.Label,
		workflow.RunEventsSubject(co.run.ID), step.ID)
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
	req := workflow.SpawnRequest{Prompt: co.briefPrompt(step), Nickname: step.Label, Job: co.run.ID}
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
	if !co.hasBrief() {
		return "", fmt.Errorf("brief step done but no brief artifact attached (stop gate)")
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

func (co *coordinator) hasBrief() bool {
	for _, a := range co.run.Artifacts {
		if a.Kind == "brief" {
			return true
		}
	}
	return false
}

// briefPrompt hands the agent the run's stop conditions so it writes a brief that
// justifies the stop, plus the reporting directive.
func (co *coordinator) briefPrompt(step *workflow.RunStep) string {
	return fmt.Sprintf(
		"Write the stopping brief for this run as an artifact of kind \"brief\".\nObjective: %s\nStop when ANY of these is met (pick the one that holds and justify it):\n- %s\n\nReport done with the brief artifact in `artifacts` and `outcome` = done or blocked.\nRUN_EVENTS=%s RUN_STEP=%s",
		co.run.Objective, joinStops(co.run.Stop),
		workflow.RunEventsSubject(co.run.ID), step.ID,
	)
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
		co.mu.Lock()
		ev, ok := co.doneEvents[stepID]
		co.mu.Unlock()
		if ok {
			return ev, true
		}
		select {
		case <-co.evCh:
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
	}()
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
