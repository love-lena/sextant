// Command sextant-workflow is the M5.4 reference workflow coordinator (TASK-26).
//
// A workflow is a CONVENTION over the two primitives, not a primitive of its own
// (ADR-0011): there is no engine in core. This coordinator is an ordinary bus
// client that runs the engine as a library and drives a declarative workflow:
//
//   - State is a sextant.workflow/v1 envelope held as an Artifact, keyed by the
//     workflow id, single-writer (this coordinator) and CAS-checkpointed — so a
//     restarted coordinator re-reads it and RESUMES at step granularity, skipping
//     steps already done (idempotent resume).
//   - A free-form event stream rides msg.workflow.<id>.events; cooperative control
//     (pause/resume/cancel/approve) rides msg.workflow.<id>.control.
//   - It COMPOSES the M5.2 dispatcher: a "dispatch" step publishes a spawn.request
//     and correlates the dispatcher's spawn.ack by requestId (request/reply over
//     pub-sub — the spawn.ack pattern, no new primitive), then waits for the
//     spawned agent to report the step done on the event stream.
//
// Layer-0 note (ADR-0012): the reserved sx.workflow.* subjects and sx_workflows
// bucket are not reachable through the current Wire API (a client publishes only
// to msg.* and writes only the ARTIFACTS bucket), so this client-side coordinator
// realizes Layer-0 over msg.* + a regular Artifact — exactly ADR-0011's "convention
// over Messages + Artifacts". See docs/demos/m5-workflow-notes.md.
//
// PoC scope: one step kind ("dispatch"); steps run sequentially (the state is a
// flat list, no dependency graph yet); single-writer-by-convention CAS (a conflict
// re-reads and retries); no engine persistence beyond the state envelope.
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

	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/conninfo"
	"github.com/oklog/ulid/v2"
)

func main() {
	fs := flag.NewFlagSet("sextant-workflow", flag.ExitOnError)
	creds := fs.String("creds", os.Getenv("SEXTANT_CREDS"), "coordinator credentials file (its own bus identity)")
	store := fs.String("store", os.Getenv("SEXTANT_STORE"), "bus store dir for bus.json discovery")
	url := fs.String("url", "", "bus URL (default: discovery file under --store)")
	plan := fs.String("plan", "", "path to a JSON workflow plan: {\"id\":\"...\",\"steps\":[{\"id\":\"\",\"kind\":\"dispatch\",\"nickname\":\"\",\"prompt\":\"\"}]}")
	id := fs.String("id", "", "workflow id (overrides the plan's; required to RESUME an existing workflow)")
	spawnSubject := fs.String("spawn-subject", "msg.topic.spawn", "subject the M5.2 dispatcher watches for spawn.request")
	stepTimeout := fs.Duration("step-timeout", 90*time.Second, "max time for one step (spawn.ack + the agent's step-done) before it fails loud")
	_ = fs.Parse(os.Args[1:])

	if *creds == "" {
		fatal("usage: sextant-workflow --creds F --store DIR (--plan F | --id ULID | <listen mode>) [--spawn-subject S] [--step-timeout D]")
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

	// Listen mode: no --plan or --id — subscribe to workflow.start and run one
	// coordinator per request (the dash's "start a workflow from a prompt" path).
	if *plan == "" && *id == "" {
		sc, sub, err := newStartConsumer(ctx, c, *spawnSubject, *stepTimeout)
		if err != nil {
			fatal("%v", err)
		}
		defer sub.Stop()
		_ = sc
		logf("coordinator up as %s; listening on %s for workflow.start", short(c.ID()), startSubject)
		select {
		case <-ctx.Done():
			logf("signalled; shutting down")
		case <-c.Drained():
			logf("bus drained; shutting down")
		}
		return
	}

	co := &coordinator{
		ctx: ctx, c: c, spawnSubject: *spawnSubject, stepTimeout: *stepTimeout,
		acks: map[string]spawnAck{}, doneEvents: map[string]bool{},
		ackCh: make(chan struct{}, 1), evCh: make(chan struct{}, 1), ctlCh: make(chan struct{}, 1),
	}
	if err := co.load(ctx, *plan, *id); err != nil {
		fatal("%v", err)
	}
	logf("coordinator up as %s; workflow %s (%d steps), status %s", short(c.ID()), co.wf.ID, len(co.wf.Steps), co.wf.Status)

	// Subscriptions: spawn.acks (to correlate dispatch steps), the workflow's event
	// stream (agents' step-done signals), and cooperative control. DeliverAll closes
	// the start race and lets a resumed coordinator see prior state.
	for _, s := range []struct {
		subj string
		h    sextant.Handler
	}{
		{*spawnSubject, co.onSpawn},
		{eventsSubject(co.wf.ID), co.onEvent},
		{controlSubject(co.wf.ID), co.onControl},
	} {
		sub, err := c.Subscribe(ctx, s.subj, s.h, sextant.DeliverAll())
		if err != nil {
			fatal("subscribe %s: %v", s.subj, err)
		}
		defer sub.Stop()
	}

	// Let DeliverAll replay any retained control settle before walking — a cancel or
	// pause issued while this coordinator was down must be honoured on (re)start, not
	// raced past into the first step.
	co.settle()

	if err := co.run(); err != nil {
		fatal("workflow %s: %v", co.wf.ID, err)
	}
}

type coordinator struct {
	ctx          context.Context
	c            *sextant.Client
	spawnSubject string
	stepTimeout  time.Duration

	wf  Workflow
	rev uint64 // current revision of the state artifact (for CAS)

	mu         sync.Mutex
	acks       map[string]spawnAck // spawn.request frame id -> ack
	doneEvents map[string]bool     // step id -> a non-self step-done event seen
	paused     bool
	cancelled  bool
	ackCh      chan struct{} // wakes waiters: new ack
	evCh       chan struct{} // wakes waiters: new event
	ctlCh      chan struct{} // wakes waiters: control changed
}

// load resumes the workflow from its state artifact if it already exists, else
// creates it from the plan. Resume is what makes the coordinator idempotent: the
// loaded statuses tell run() which steps to skip.
func (co *coordinator) load(ctx context.Context, planPath, idFlag string) error {
	// Determine the id: the flag wins (to resume a known workflow), else the plan's,
	// else a fresh ULID.
	var planWF Workflow
	if planPath != "" {
		b, err := os.ReadFile(planPath)
		if err != nil {
			return fmt.Errorf("read plan: %w", err)
		}
		if err := json.Unmarshal(b, &planWF); err != nil {
			return fmt.Errorf("parse plan: %w", err)
		}
	}
	id := idFlag
	if id == "" {
		id = planWF.ID
	}
	if id == "" {
		id = ulid.Make().String()
	}

	if art, err := co.c.GetArtifact(ctx, stateName(id)); err == nil {
		if wf, ok := parseWorkflow(art.Record); ok {
			co.wf, co.rev = wf, art.Revision
			co.wf.Owner = co.c.ID() // (re)own on resume
			if isTerminal(co.wf.Status) {
				return nil // done/cancelled/failed is terminal; run() honours it
			}
			co.wf.Status = wfRunning
			return co.checkpoint()
		}
	}

	// Fresh workflow from the plan.
	if planPath == "" {
		return fmt.Errorf("workflow %s not found and no --plan given to create it", id)
	}
	co.wf = Workflow{ID: id, Status: wfRunning, Owner: co.c.ID(), Steps: planWF.Steps}
	for i := range co.wf.Steps {
		if co.wf.Steps[i].Status == "" {
			co.wf.Steps[i].Status = stepPending
		}
	}
	rev, err := co.c.CreateArtifact(ctx, stateName(id), co.wf.marshal())
	if err != nil {
		return fmt.Errorf("create workflow state: %w", err)
	}
	co.rev = rev
	return nil
}

// loadDirect initialises a fresh workflow from a pre-built Workflow struct
// (bypasses file I/O). It is the start-consumer's entry point: the consumer
// constructs a Workflow from the workflow.start request fields and hands it
// straight to the coordinator, reusing the same CAS + artifact machinery.
func (co *coordinator) loadDirect(ctx context.Context, wf Workflow) error {
	for i := range wf.Steps {
		if wf.Steps[i].Status == "" {
			wf.Steps[i].Status = stepPending
		}
	}
	wf.Status = wfRunning
	wf.Owner = co.c.ID()
	rev, err := co.c.CreateArtifact(ctx, stateName(wf.ID), wf.marshal())
	if err != nil {
		return fmt.Errorf("create workflow state: %w", err)
	}
	co.wf, co.rev = wf, rev
	return nil
}

// isTerminal reports whether a workflow status is final — there is no more work
// to walk. A resumed coordinator that loads a terminal workflow does nothing
// (so a cancelled or failed workflow is never re-run on restart).
func isTerminal(status string) bool {
	return status == wfDone || status == wfCancelled || status == wfFailed
}

// run walks the steps: gate on cooperative control, find the next not-done step,
// run it, checkpoint + emit. A resumed coordinator skips done steps for free, and
// a resumed TERMINAL workflow (done/cancelled/failed) is a no-op.
func (co *coordinator) run() error {
	if isTerminal(co.wf.Status) {
		logf("workflow %s already %s; nothing to do", co.wf.ID, co.wf.Status)
		return nil
	}
	for {
		if co.isCancelled() {
			return co.finish(wfCancelled)
		}
		co.waitWhilePaused()
		if co.ctx.Err() != nil {
			logf("signalled; leaving workflow %s resumable at its checkpoint", co.wf.ID)
			return nil
		}
		if co.isCancelled() {
			return co.finish(wfCancelled)
		}
		idx := co.wf.nextPending()
		if idx == -1 {
			return co.finish(wfDone)
		}
		step := &co.wf.Steps[idx]
		logf("step %s (%s): running", step.ID, step.Kind)
		if err := co.runStep(idx); err != nil {
			step.Status = stepFailed
			if cerr := co.checkpoint(); cerr != nil {
				logf("warn: checkpoint after step %s failed: %v", step.ID, cerr)
			}
			co.emit(WorkflowEvent{Step: step.ID, Status: stepFailed, Note: err.Error()})
			co.wf.Status = wfFailed
			if cerr := co.checkpoint(); cerr != nil {
				logf("warn: checkpoint of failed status: %v", cerr)
			}
			co.emit(WorkflowEvent{Status: wfFailed, Note: "step " + step.ID + " failed"})
			return fmt.Errorf("step %s: %w", step.ID, err)
		}
		step.Status = stepDone
		if err := co.checkpoint(); err != nil {
			return err
		}
		co.emit(WorkflowEvent{Step: step.ID, Status: stepDone, By: step.Agent})
		logf("step %s: done (agent %s)", step.ID, short(step.Agent))
	}
}

// finish records a terminal workflow status and emits it.
func (co *coordinator) finish(status string) error {
	co.wf.Status = status
	if err := co.checkpoint(); err != nil {
		return err
	}
	co.emit(WorkflowEvent{Status: status})
	logf("workflow %s: %s", co.wf.ID, status)
	return nil
}

func (co *coordinator) runStep(idx int) error {
	step := &co.wf.Steps[idx]
	step.Status = stepRunning
	if err := co.checkpoint(); err != nil {
		return err
	}
	co.emit(WorkflowEvent{Step: step.ID, Status: stepRunning})
	switch step.Kind {
	case "dispatch":
		return co.runDispatch(step)
	default:
		return fmt.Errorf("unknown step kind %q", step.Kind)
	}
}

// runDispatch composes the M5.2 dispatcher: publish a spawn.request, correlate the
// spawn.ack by requestId, then wait for the spawned agent to report the step done
// on the event stream. Each wait is bounded (fail-loud, never a silent hang).
//
// KNOWN (PoC): not crash-safe for an in-flight step. If the coordinator dies after
// checkpointing stepRunning but before the done-event, resume re-dispatches the
// step — and DeliverAll may replay the prior attempt's done-event, so step.Agent
// can name the new agent while the recorded done came from the old one. Crash-safe
// in-flight steps (a fencing token / dedup by attempt) are out of PoC scope.
func (co *coordinator) runDispatch(step *Step) error {
	req := spawnRequest{Prompt: co.dispatchPrompt(step), Nickname: step.Nickname, Job: co.wf.ID}
	out, err := co.c.PublishMsg(co.ctx, co.spawnSubject, req.marshal())
	if err != nil {
		return fmt.Errorf("publish spawn.request: %w", err)
	}
	ack, ok := co.awaitAck(out.ID, co.stepTimeout)
	if !ok {
		return fmt.Errorf("no spawn.ack within %s", co.stepTimeout)
	}
	if ack.Status != "ok" {
		return fmt.Errorf("dispatch rejected: %s", ack.Error)
	}
	step.Agent = ack.ID
	if !co.awaitStepDone(step.ID, co.stepTimeout) {
		return fmt.Errorf("agent %s never reported step %q done within %s", short(ack.ID), step.ID, co.stepTimeout)
	}
	return nil
}

// dispatchPrompt augments the step's task with the reporting directive the spawned
// agent uses to signal completion back to this workflow's event stream.
func (co *coordinator) dispatchPrompt(step *Step) string {
	return fmt.Sprintf("%s\nWF_EVENTS=%s WF_STEP=%s", step.Prompt, eventsSubject(co.wf.ID), step.ID)
}

// checkpoint persists the current state envelope with CAS on the tracked revision.
// Single-writer by convention, so a conflict is rare; on one we re-read the
// revision and retry (last-writer-wins for our own envelope).
func (co *coordinator) checkpoint() error {
	for attempt := 0; attempt < 5; attempt++ {
		rev, err := co.c.UpdateArtifact(co.ctx, stateName(co.wf.ID), co.wf.marshal(), co.rev)
		if err == nil {
			co.rev = rev
			return nil
		}
		art, gerr := co.c.GetArtifact(co.ctx, stateName(co.wf.ID))
		if gerr != nil {
			return fmt.Errorf("checkpoint %s: %w", co.wf.ID, err)
		}
		co.rev = art.Revision
	}
	return fmt.Errorf("checkpoint %s: exhausted CAS retries", co.wf.ID)
}

func (co *coordinator) emit(ev WorkflowEvent) {
	_ = co.c.Publish(co.ctx, eventsSubject(co.wf.ID), ev.marshal())
}

// --- subscription handlers (run on the SDK's delivery goroutines) ---

func (co *coordinator) onSpawn(m sextant.Message) {
	a, ok := parseSpawnAck(m.Frame.Record)
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
		return // our own emitted events are not step-done signals
	}
	ev, ok := parseWorkflowEvent(m.Frame.Record)
	if !ok || ev.Step == "" || ev.Status != stepDone {
		return
	}
	co.mu.Lock()
	co.doneEvents[ev.Step] = true
	co.mu.Unlock()
	wake(co.evCh)
}

func (co *coordinator) onControl(m sextant.Message) {
	ctl, ok := parseWorkflowControl(m.Frame.Record)
	if !ok {
		return
	}
	co.mu.Lock()
	switch ctl.Verb {
	case ctlPause:
		co.paused = true
	case ctlResume, ctlApprove:
		co.paused = false
	case ctlCancel:
		co.cancelled = true
	}
	co.mu.Unlock()
	logf("control: %s", ctl.Verb)
	co.emit(WorkflowEvent{Status: "control", Note: ctl.Verb})
	wake(co.ctlCh)
}

// --- bounded waits ---

func (co *coordinator) awaitAck(requestID string, timeout time.Duration) (spawnAck, bool) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	for {
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
		case <-t.C:
			return spawnAck{}, false
		case <-co.ctx.Done():
			return spawnAck{}, false
		}
	}
}

func (co *coordinator) awaitStepDone(stepID string, timeout time.Duration) bool {
	t := time.NewTimer(timeout)
	defer t.Stop()
	for {
		co.mu.Lock()
		done := co.doneEvents[stepID]
		co.mu.Unlock()
		if done {
			return true
		}
		select {
		case <-co.evCh:
		case <-t.C:
			return false
		case <-co.ctx.Done():
			return false
		}
	}
}

func (co *coordinator) waitWhilePaused() {
	wasPaused := false
	for {
		co.mu.Lock()
		paused, cancelled := co.paused, co.cancelled
		co.mu.Unlock()
		if cancelled {
			return // run() records the cancel; finish() overwrites any paused status
		}
		if !paused {
			if wasPaused { // resumed — reflect it in the observable envelope
				co.wf.Status = wfRunning
				if err := co.checkpoint(); err != nil {
					logf("warn: checkpoint on resume: %v", err)
				}
				co.emit(WorkflowEvent{Status: wfRunning, Note: "resumed"})
			}
			return
		}
		if !wasPaused { // entering pause — make it visible to anyone watching the envelope
			wasPaused = true
			co.wf.Status = wfPaused
			if err := co.checkpoint(); err != nil {
				logf("warn: checkpoint on pause: %v", err)
			}
			co.emit(WorkflowEvent{Status: wfPaused})
			logf("paused; awaiting resume/cancel")
		}
		select {
		case <-co.ctlCh:
		case <-co.ctx.Done():
			return
		}
	}
}

func (co *coordinator) isCancelled() bool {
	co.mu.Lock()
	defer co.mu.Unlock()
	return co.cancelled
}

// settle gives DeliverAll a moment to replay retained control before the walk
// begins, so a cancel/pause issued while the coordinator was down is honoured
// rather than raced past into the first step. NOTE: this is a local-bus heuristic,
// not a guaranteed barrier — on a slow/remote bus the replay may land after it. A
// production coordinator should gate on an explicit drain signal or re-read the
// control subject before the first step.
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

// --- workflow.start consumer ---

// startConsumer subscribes to msg.topic.workflow.start with DeliverAll, dedups
// by frame ID (in-memory, PoC scope mirrors the dispatcher), and runs exactly
// one workflow per fresh request. It acks every handled frame — success or
// failure — so requesters are never left waiting on silence (fail-loud).
type startConsumer struct {
	ctx          context.Context
	c            *sextant.Client
	spawnSubject string
	stepTimeout  time.Duration

	mu   sync.Mutex
	seen map[string]bool // request frame ids already handled (dedup across DeliverAll replay)
}

// newStartConsumer builds and subscribes a startConsumer on startSubject.
// It returns the consumer and its subscription (caller defers sub.Stop()).
func newStartConsumer(ctx context.Context, c *sextant.Client, spawnSubject string, stepTimeout time.Duration) (*startConsumer, sextant.Subscription, error) {
	sc := &startConsumer{
		ctx: ctx, c: c, spawnSubject: spawnSubject, stepTimeout: stepTimeout,
		seen: map[string]bool{},
	}
	sub, err := c.Subscribe(ctx, startSubject, sc.handle, sextant.DeliverAll())
	if err != nil {
		return nil, nil, fmt.Errorf("subscribe %s: %w", startSubject, err)
	}
	return sc, sub, nil
}

// handle processes one frame on startSubject. It acts only on workflow.start
// records (ignoring its own echoed workflow.start.acks and anything else),
// dedups by request frame id, creates the workflow, ACKS IMMEDIATELY (accepted
// + workflowId), then runs the coordinator in a goroutine so the handler does
// not block the delivery loop for the duration of the run.
//
// The ack means "accepted/started"; run failures surface on the workflow's
// msg.workflow.<id>.events stream (the coordinator emits a failed status event),
// NOT via a second ack. ack status:error is ONLY for couldn't-start failures
// (loadDirect / artifact-create) that occur before the goroutine launches.
func (sc *startConsumer) handle(m sextant.Message) {
	req, ok := parseWorkflowStartRequest(m.Frame.Record)
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

	logf("workflow.start %s from %s: prompt=%q nick=%q", short(reqID), short(m.Frame.Author), req.Prompt, req.Nickname)

	co, wfID, stopSubs, err := sc.prepareWorkflow(req)
	ack := WorkflowStartAck{Nonce: req.Nonce, RequestID: reqID}
	if err != nil {
		ack.Status = statusError
		ack.Error = err.Error()
		logf("workflow.start %s: prepare failed: %v", short(reqID), err)
	} else {
		ack.Status = statusOK
		ack.WorkflowID = wfID
		logf("workflow.start %s: accepted, workflow %s", short(reqID), short(wfID))
	}
	// Publish the ack BEFORE launching the run — the dash needs workflowId
	// promptly to subscribe to the events stream; the run may take minutes.
	if err := sc.c.Publish(context.Background(), startSubject, ack.marshal()); err != nil {
		logf("publish workflow.start.ack: %v", err)
	}
	if co == nil {
		return // couldn't-start: already acked error above
	}
	// Run the coordinator asynchronously so handle() returns immediately,
	// unblocking the delivery goroutine for the next request.
	// stopSubs is deferred inside the goroutine so the helper subscriptions
	// (spawn, events, control) are stopped once the run ends — preventing
	// unbounded accumulation across sequential runs on a long-lived consumer.
	go func() {
		defer stopSubs()
		if err := co.run(); err != nil {
			logf("workflow %s run error: %v", short(wfID), err)
		}
	}()
}

// prepareWorkflow creates the workflow state artifact and subscribes the
// coordinator's helper subjects. It returns the coordinator (ready to run),
// the workflow id, a stopSubs closure that stops the helper subscriptions, and
// any error. On failure it returns (nil, "", noop, err).
//
// The caller MUST call stopSubs() after run() ends (typically via defer in the
// run goroutine) to prevent subscription accumulation across sequential runs on
// a long-lived consumer.
func (sc *startConsumer) prepareWorkflow(req WorkflowStartRequest) (*coordinator, string, func(), error) {
	wfID := ulid.Make().String()
	nick := req.Nickname
	if nick == "" {
		nick = "step-" + short(wfID)
	}
	wf := Workflow{
		ID: wfID,
		Steps: []Step{
			{ID: "run", Kind: "dispatch", Nickname: nick, Prompt: req.Prompt},
		},
	}
	co := &coordinator{
		ctx: sc.ctx, c: sc.c, spawnSubject: sc.spawnSubject, stepTimeout: sc.stepTimeout,
		acks: map[string]spawnAck{}, doneEvents: map[string]bool{},
		ackCh: make(chan struct{}, 1), evCh: make(chan struct{}, 1), ctlCh: make(chan struct{}, 1),
	}
	if err := co.loadDirect(sc.ctx, wf); err != nil {
		return nil, wfID, func() {}, err
	}

	// Subscribe the coordinator's helper subjects for this run's lifetime.
	// The handles are returned as a stop closure so the run goroutine can
	// clean them up after co.run() returns — preventing unbounded accumulation
	// across sequential runs (the old `defer sub.Stop()` in the synchronous
	// path was correct; this restores that guarantee for the async path).
	var subs []sextant.Subscription
	for _, s := range []struct {
		subj string
		h    sextant.Handler
	}{
		{sc.spawnSubject, co.onSpawn},
		{eventsSubject(wfID), co.onEvent},
		{controlSubject(wfID), co.onControl},
	} {
		sub, err := sc.c.Subscribe(sc.ctx, s.subj, s.h, sextant.DeliverAll())
		if err != nil {
			for _, s := range subs {
				s.Stop()
			}
			return nil, wfID, func() {}, fmt.Errorf("subscribe %s: %w", s.subj, err)
		}
		subs = append(subs, sub)
	}
	stopSubs := func() {
		for _, s := range subs {
			s.Stop()
		}
	}
	co.settle()
	return co, wfID, stopSubs, nil
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
