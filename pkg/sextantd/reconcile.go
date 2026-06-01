package sextantd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/containermgr"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// Default reconcile cadence + debounce constants (RFC §8).
const (
	// DefaultSweepInterval is the periodic full-reconcile cadence — the
	// backstop that catches deaths the docker watcher missed (RFC §5.1,
	// §8: 30–60s). One enqueue of every agent per tick.
	DefaultSweepInterval = 45 * time.Second
	// DefaultDieDebounce is how long after an observed container `die` the
	// reconciler suppresses a daemon-inferred `lost`, so a clean sidecar
	// shutdown (whose `ended` lands shortly after the container exits)
	// wins the precedence contest (RFC §8: 5s). Carried forward from the
	// L3 container watcher.
	DefaultDieDebounce = 5 * time.Second
	// reconcileUpdateTimeout caps a single reconcile pass's KV/docker IO.
	reconcileUpdateTimeout = 30 * time.Second
	// reconcileCASRetries caps the status-write CAS retry budget. The
	// reconciler RETRIES on conflict (it is a background loop — RFC §5.8:
	// "reconciler writes retry-rebase on conflict; only operator RPCs
	// surface the 409 to a human").
	reconcileCASRetries = 5
)

// Recovery safety-rail constants (RFC §8). These govern the P1 recovery
// branch — auto-restart of an involuntarily-lost agent. They are the
// k8s-calibrated, agent-adjusted defaults; every one is exercised under
// the injected clock (Reconciler.Now) so the schedule is deterministic in
// tests (RFC §5.9).
const (
	// RecoveryBackoffInitial is the first wait before re-actuating a
	// lost/crashed agent. The schedule is RecoveryBackoffInitial ×
	// RecoveryBackoffFactor each restart, capped at RecoveryBackoffCap:
	// 10 → 20 → 40 → 80 → 160 → 300 (RFC §8). No per-item jitter (a
	// multi-node concern).
	RecoveryBackoffInitial = 10 * time.Second
	// RecoveryBackoffFactor is the exponential growth factor (×2).
	RecoveryBackoffFactor = 2
	// RecoveryBackoffCap is the maximum backoff wait (RFC §8: 300s).
	RecoveryBackoffCap = 300 * time.Second
	// RecoveryBackoffReset is how long an agent must run CONTINUOUSLY
	// before its backoff counter (the windowed crash count) resets. RFC
	// §8 is emphatic this is an INDEPENDENT constant, NOT 2×the cap —
	// KEP-4603's own evolution proves coupling them is a trap.
	RecoveryBackoffReset = 10 * time.Minute
	// RecoveryStableRun is the minimum continuous run that counts as
	// "stable" for the reset (RFC §8: ≥30s) — without it an agent whose
	// container exits right after start would reset its budget every loop.
	RecoveryStableRun = 30 * time.Second
	// CrashBudgetLimit is the windowed restart budget: more than this many
	// auto-restarts inside CrashBudgetWindow flips the agent to terminal
	// `crashed` (CrashLoopBackOff) and stops auto-restarting (RFC §8).
	CrashBudgetLimit = 5
	// CrashBudgetWindow is the crash-budget window (RFC §8: 10 min).
	CrashBudgetWindow = 10 * time.Minute
	// LivenessFailureThreshold is the consecutive health-check failure
	// count that trips the restart path for a wedged-but-running agent
	// (RFC §8: 3 consecutive failures).
	LivenessFailureThreshold = 3
	// LivenessPeriod is the health-check period (RFC §8: 10s). The
	// reconciler treats a heartbeat older than this as one failed probe.
	LivenessPeriod = 10 * time.Second
)

// HeartbeatLookup is the narrow surface the reconciler uses for the
// liveness probe — the last time a heartbeat was seen for an agent. The
// in-memory HeartbeatCache (heartbeat_cache.go) satisfies it. Nil-safe:
// a reconciler with no lookup simply skips the liveness probe.
type HeartbeatLookup interface {
	LastSeen(agentID uuid.UUID) (time.Time, bool)
}

// ReconcileDefsKV is the read+write KV surface the reconciler needs on
// agent_definitions. The reconciler is the SOLE writer of status, and it
// retry-rebases on CAS conflict rather than bailing.
type ReconcileDefsKV interface {
	Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error)
	ListKeys(ctx context.Context, opts ...jetstream.WatchOpt) (jetstream.KeyLister, error)
	Update(ctx context.Context, key string, value []byte, revision uint64) (uint64, error)
}

// ContainerObserver is the narrow surface the reconciler uses to
// re-observe actual container reality (List by label). Tests substitute
// a fake docker.
type ContainerObserver interface {
	List(ctx context.Context, f containermgr.Filter) ([]containermgr.ContainerInfo, error)
}

// reconcileActuator is the surface the reconciler drives — the sole
// actuator (handlers.Actuator satisfies it). Splitting it as an
// interface keeps the reconciler unit-testable with a fake actuator.
//
// DesiredFingerprint recomputes the desired spec identity for the P2
// drift compare (RFC §5.6, §5.8) via the SAME C0 builder the actuation
// uses — comparing OUR recomputed fingerprint (never docker's live spec)
// is what keeps drift detection free of false positives.
type reconcileActuator interface {
	Actuate(ctx context.Context, def sextantproto.AgentDefinition, resumeSession bool) (handlers.ActuateResult, error)
	Stop(ctx context.Context, def sextantproto.AgentDefinition) error
	Teardown(ctx context.Context, def sextantproto.AgentDefinition) error
	DesiredFingerprint(ctx context.Context, def sextantproto.AgentDefinition) (handlers.DesiredSpecID, error)
}

// Reconciler is the spine (RFC §5.1): one idempotent, level-triggered
// reconcile that is BOTH the sole writer of observed status AND (via the
// Actuator) the sole actuator of the container runtime. The three
// sensors (L1 heartbeat, L3 die-watcher, periodic sweep) are hint
// sources that Enqueue; they never write status.
type Reconciler struct {
	Defs       ReconcileDefsKV
	Containers ContainerObserver
	Actuator   reconcileActuator
	HostID     string

	// Heartbeats is the liveness probe source (RFC §8 P1 liveness). When
	// non-nil, the reconciler treats a heartbeat staler than LivenessPeriod
	// as one failed health-check; LivenessFailureThreshold consecutive
	// failures route a still-running agent through the restart path. Nil
	// disables the probe (a still-running agent is assumed healthy).
	Heartbeats HeartbeatLookup

	// SweepInterval is the periodic full-reconcile cadence (default
	// DefaultSweepInterval). DieDebounce is the lost-suppression window
	// after an observed die (default DefaultDieDebounce).
	SweepInterval time.Duration
	DieDebounce   time.Duration

	// Now is injected for deterministic tests.
	Now func() time.Time

	queue *workQueue

	mu sync.Mutex
	// sidecarTerminal tracks incarnations for which a sidecar terminal
	// (ended/crashed) was observed — the "sidecar-observed terminal
	// OUTRANKS daemon-inferred lost" precedence (carried from the
	// lifecycle watcher). Keyed by incarnation id.
	sidecarTerminal map[uuid.UUID]sextantproto.ObservedState
	// dieAt records the wall-clock of the most recent observed container
	// `die` per incarnation — the 5s debounce window. Keyed by incarnation
	// id.
	dieAt map[uuid.UUID]time.Time
	// atTurnBoundary tracks whether each incarnation is currently parked at
	// a TURN BOUNDARY — the only moment the P2 drift branch may converge a
	// drifted-but-healthy agent by restart (RFC §5.6: "never mid-turn").
	// The sidecar emits no turn-START signal, only `lifecycle.turn_ended`,
	// so the model is: a freshly-started incarnation is mid (its initial
	// turn) → absent from the map = false; `turn_ended` sets it true (idle,
	// awaiting the next prompt); `started`/`resumed` re-arm it to false.
	// Keyed by incarnation id, so a fresh incarnation starts mid-turn with
	// no stale carry-over. The drift restart waits for true.
	atTurnBoundary map[uuid.UUID]bool
}

// NewReconciler constructs a Reconciler with its work queue ready from a
// value template (the cfg argument carries only the externally-set
// fields; the mutex + maps are initialized here). The caller starts it
// with Run (the worker + sweep ticker).
func NewReconciler(cfg *Reconciler) *Reconciler {
	r := &Reconciler{
		Defs:          cfg.Defs,
		Containers:    cfg.Containers,
		Actuator:      cfg.Actuator,
		HostID:        cfg.HostID,
		Heartbeats:    cfg.Heartbeats,
		SweepInterval: cfg.SweepInterval,
		DieDebounce:   cfg.DieDebounce,
		Now:           cfg.Now,
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.SweepInterval <= 0 {
		r.SweepInterval = DefaultSweepInterval
	}
	if r.DieDebounce <= 0 {
		r.DieDebounce = DefaultDieDebounce
	}
	r.queue = newWorkQueue()
	r.sidecarTerminal = make(map[uuid.UUID]sextantproto.ObservedState)
	r.dieAt = make(map[uuid.UUID]time.Time)
	r.atTurnBoundary = make(map[uuid.UUID]bool)
	return r
}

// Enqueue is the hint sink the three sensors call. Level-triggered: the
// reconcile body re-reads everything, so the hint carries no payload —
// only an agent identity (RFC §3.2).
func (r *Reconciler) Enqueue(agentID uuid.UUID) {
	if agentID == uuid.Nil {
		return
	}
	r.queue.Enqueue(agentID)
}

// OnDie is the L3 docker `die`-watcher hint path. It records the die
// timestamp (the 5s debounce anchor) and enqueues a reconcile. The
// reconciler does NOT mark lost until the debounce elapses without a
// sidecar terminal (carried-forward race-hardening).
func (r *Reconciler) OnDie(agentID, incarnationID uuid.UUID) {
	if incarnationID != uuid.Nil {
		r.mu.Lock()
		r.dieAt[incarnationID] = r.Now()
		r.mu.Unlock()
	}
	r.Enqueue(agentID)
	// Re-enqueue after the debounce so a die with no following sidecar
	// terminal converges to lost without waiting for the periodic sweep.
	go func() {
		t := time.NewTimer(r.DieDebounce + 100*time.Millisecond)
		defer t.Stop()
		<-t.C
		r.Enqueue(agentID)
	}()
}

// OnSidecarLifecycle is the hint path for sidecar-driven lifecycle
// envelopes. A terminal transition (ended/crashed) records the
// precedence flag (so the reconciler will not downgrade it to lost) and
// enqueues. A `turn_ended` parks the incarnation at a turn boundary (the
// P2 drift branch may now converge it by restart); a `started`/`resumed`
// re-arms it mid-turn. Every transition enqueues so the reconciler
// re-reads desired + re-observes actual and converges (sole writer).
func (r *Reconciler) OnSidecarLifecycle(p sextantproto.LifecyclePayload) {
	if p.AgentUUID == uuid.Nil {
		return
	}
	if obs, ok := sidecarTerminalObserved(p.Transition); ok && p.IncarnationID != uuid.Nil {
		r.mu.Lock()
		r.sidecarTerminal[p.IncarnationID] = obs
		// A sidecar terminal cancels any pending die-debounce: the cause is
		// now observed, so there is nothing to infer.
		delete(r.dieAt, p.IncarnationID)
		r.mu.Unlock()
	}
	// Turn-boundary tracking for the drift branch (RFC §5.6: converge a
	// drifted agent ONLY at a turn boundary). turn_ended → idle at a
	// boundary (safe to restart); started/resumed → re-entered a turn.
	if p.IncarnationID != uuid.Nil {
		switch p.Transition {
		case sextantproto.LifecycleTurnEnded:
			r.mu.Lock()
			r.atTurnBoundary[p.IncarnationID] = true
			r.mu.Unlock()
		case sextantproto.LifecycleStarted, sextantproto.LifecycleResumedEvent:
			r.mu.Lock()
			r.atTurnBoundary[p.IncarnationID] = false
			r.mu.Unlock()
		}
	}
	r.Enqueue(p.AgentUUID)
}

// forgetIncarnation drops the in-memory per-incarnation hint state for a
// superseded incarnation (terminal precedence, die-debounce anchor,
// turn-boundary flag) so the maps don't grow unbounded across restarts.
// Called when a fresh incarnation is actuated; the old id is dead and its
// hints are meaningless. Nil id is a no-op.
func (r *Reconciler) forgetIncarnation(incID uuid.UUID) {
	if incID == uuid.Nil {
		return
	}
	r.mu.Lock()
	delete(r.sidecarTerminal, incID)
	delete(r.dieAt, incID)
	delete(r.atTurnBoundary, incID)
	r.mu.Unlock()
}

// sidecarTerminalObserved maps a wire lifecycle transition to the
// observed-state it implies, returning ok=false for non-terminal
// transitions. ended → ended; crashed → crashed.
func sidecarTerminalObserved(t sextantproto.LifecycleEvent) (sextantproto.ObservedState, bool) {
	switch t {
	case sextantproto.LifecycleEnded:
		return sextantproto.ObservedEnded, true
	case sextantproto.LifecycleCrashedEvent:
		return sextantproto.ObservedCrashed, true
	default:
		return "", false
	}
}

// Run starts the single reconcile worker + the periodic sweep ticker and
// blocks until ctx is cancelled. One worker (RFC §5.1) serializes
// reconciles so they don't race the docker socket.
func (r *Reconciler) Run(ctx context.Context) error {
	// Periodic sweep: enqueue a full reconcile of every agent each tick —
	// the backstop for missed events (RFC §5.1).
	go r.sweepLoop(ctx)
	// One immediate sweep so a daemon restart converges right away (our
	// equivalent of k8s's post-relist).
	r.sweep(ctx)

	go func() {
		<-ctx.Done()
		r.queue.Shutdown()
	}()

	for {
		id, shut := r.queue.Get()
		if shut {
			return ctx.Err()
		}
		r.processOne(ctx, id)
		r.queue.Done(id)
		if ctx.Err() != nil {
			r.queue.Shutdown()
			return ctx.Err()
		}
	}
}

// sweepLoop ticks the periodic full sweep.
func (r *Reconciler) sweepLoop(ctx context.Context) {
	t := time.NewTicker(r.SweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.sweep(ctx)
		}
	}
}

// sweep enqueues a reconcile for every agent in the bucket.
func (r *Reconciler) sweep(ctx context.Context) {
	sctx, cancel := context.WithTimeout(ctx, reconcileUpdateTimeout)
	defer cancel()
	lister, err := r.Defs.ListKeys(sctx)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrNoKeysFound) {
			return
		}
		log.Printf("sextantd: reconciler sweep: list keys: %v", err)
		return
	}
	defer func() { _ = lister.Stop() }()
	for key := range lister.Keys() {
		id, err := uuid.Parse(key)
		if err != nil {
			continue
		}
		r.Enqueue(id)
	}
}

// processOne runs one reconcile pass for agentID. It is the imperative
// shell around the pure decideAction core: re-read desired, re-observe
// actual, decide, act, write observed status (sole writer). Errors are
// logged + the agent re-enqueued (the periodic sweep is the backstop).
func (r *Reconciler) processOne(ctx context.Context, agentID uuid.UUID) {
	pctx, cancel := context.WithTimeout(ctx, reconcileUpdateTimeout)
	defer cancel()
	requeueAfter, err := r.reconcileOnce(pctx, agentID)
	if err != nil {
		log.Printf("sextantd: reconcile %s: %v", agentID, err)
	}
	// A recovery hold-off (backoff not yet elapsed) asks to be re-checked
	// when the deadline passes, rather than waiting for the next periodic
	// sweep — so the backoff schedule (10s ×2) is actually observed and a
	// lost agent self-heals promptly. Distinct from the sweep path (routine
	// re-checks) per RFC §5.1.
	if requeueAfter > 0 {
		r.requeueAfter(ctx, agentID, requeueAfter)
	}
}

// requeueAfter enqueues agentID once after d, unless ctx is cancelled
// first. It mirrors the OnDie debounce-requeue pattern (a single bounded
// timer goroutine, no unbounded fan-out).
func (r *Reconciler) requeueAfter(ctx context.Context, agentID uuid.UUID, d time.Duration) {
	go func() {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
			r.Enqueue(agentID)
		}
	}()
}

// reconcileOne runs one reconcile pass for agentID, discarding the
// requeue hint. The package's own tests drive single passes through this
// thin wrapper (they advance the injected clock + re-call rather than
// relying on a timed requeue).
func (r *Reconciler) reconcileOne(ctx context.Context, agentID uuid.UUID) error {
	_, err := r.reconcileOnce(ctx, agentID)
	return err
}

// reconcileOnce is the single-pass reconcile core. It re-reads desired,
// re-observes actual, decides, acts, and writes observed status (sole
// writer). It returns a requeue hint: a positive duration means "re-check
// me after this delay" (a recovery hold-off waiting on the backoff
// deadline); zero means "the periodic sweep is sufficient."
func (r *Reconciler) reconcileOnce(ctx context.Context, agentID uuid.UUID) (time.Duration, error) {
	entry, err := r.Defs.Get(ctx, agentID.String())
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return 0, nil // agent purged; nothing to converge
		}
		return 0, fmt.Errorf("get: %w", err)
	}
	var def sextantproto.AgentDefinition
	if err := json.Unmarshal(entry.Value(), &def); err != nil {
		return 0, fmt.Errorf("decode: %w", err)
	}

	actual, err := r.observe(ctx, def)
	if err != nil {
		return 0, fmt.Errorf("observe: %w", err)
	}
	// Compute the time-dependent recovery verdict under the injected clock
	// and hand it to the (pure, clock-free) decision core (RFC §5.9).
	actual.Recovery = r.computeRecovery(def, actual)

	dec := decideAction(def, actual)
	rec := actual.Recovery
	requeue := r.recoveryRequeue(def, dec, rec)

	switch dec.Action {
	case actionNone:
		// Converge observed status only if the decision says so (e.g.
		// container is up but status still says pending → running).
		return requeue, r.writeObserved(ctx, agentID, statusWrite{dec: dec, rec: rec})

	case actionActuate:
		res, aerr := r.Actuator.Actuate(ctx, def, resumeSessionFor(def))
		if aerr != nil {
			return 0, fmt.Errorf("actuate: %w", aerr)
		}
		// Stamp the fresh incarnation + caught-up generation/nonce so the
		// next pass is converged (idempotence). Clear stale per-incarnation
		// hint state for the (now superseded) old incarnation — including its
		// turn-boundary flag, so it neither leaks nor lingers.
		r.forgetIncarnation(def.Status.CurrentIncarnationID)
		return requeue, r.writeObserved(ctx, agentID, statusWrite{
			dec:            dec,
			rec:            rec,
			newIncarnation: res.IncarnationID,
			gen:            def.Spec.Generation,
			nonce:          def.Spec.ReactuationNonce,
		})

	case actionStop:
		if serr := r.Actuator.Stop(ctx, def); serr != nil {
			return 0, fmt.Errorf("stop: %w", serr)
		}
		return requeue, r.writeObserved(ctx, agentID, statusWrite{dec: dec, rec: rec})

	case actionTeardown:
		if terr := r.Actuator.Teardown(ctx, def); terr != nil {
			return 0, fmt.Errorf("teardown: %w", terr)
		}
		return requeue, r.writeObserved(ctx, agentID, statusWrite{dec: dec, rec: rec})

	case actionMarkLost:
		return requeue, r.writeObserved(ctx, agentID, statusWrite{dec: dec, rec: rec})

	default:
		return 0, nil
	}
}

// recoveryRequeue returns how long to wait before re-checking an agent
// that the recovery branch is holding off (backoff not yet elapsed), so
// the restart fires near the backoff deadline instead of on the next
// periodic sweep. Zero when no recovery hold-off applies — every other
// path relies on the sweep + event hints. It re-derives the same
// timing computeRecovery used, so the two never disagree.
func (r *Reconciler) recoveryRequeue(def sextantproto.AgentDefinition, dec decision, rec recoveryInputs) time.Duration {
	// Only a held-off recoverable terminal asks for a timed re-check: the
	// decision did nothing (action none), the agent is in a terminal the
	// policy recovers, it is under budget, and the backoff has not elapsed.
	if dec.Action != actionNone || def.Spec.Desired != sextantproto.DesiredRun {
		return 0
	}
	if !def.Status.Observed.IsTerminal() || !policyRecovers(def.Spec.RestartPolicy, def.Status.Observed) {
		return 0
	}
	if rec.BudgetExhausted || rec.BackoffElapsed {
		return 0
	}
	if def.Status.LastExit == nil || def.Status.LastExit.At.IsZero() {
		// No anchor yet (the loss was only just marked this pass). The
		// MarkLost write stamps LastExit; a follow-up enqueue happens via the
		// die-hint requeue / sweep, after which the anchor exists.
		return 0
	}
	count := 0
	if !def.Status.CrashWindow.Since.IsZero() &&
		r.Now().Sub(def.Status.CrashWindow.Since.Time) < CrashBudgetWindow {
		count = def.Status.CrashWindow.Count
	}
	remaining := backoffFor(count+1) - r.Now().Sub(def.Status.LastExit.At.Time)
	if remaining <= 0 {
		return 0
	}
	// A small slop so the re-check lands just after the deadline, not on it.
	return remaining + 100*time.Millisecond
}

// resumeSessionFor decides whether the actuation should resume the SDK
// session. The restart handler clears Spec.Runtime.SessionID for a fresh
// session and keeps it for a preserved one, so "session id present" is
// the resume signal.
func resumeSessionFor(def sextantproto.AgentDefinition) bool {
	return def.Spec.Runtime.SessionID != nil
}

// computeRecovery derives the time-dependent recovery verdict for the
// current pass (RFC §8). It is the ONLY place in the decision path that
// reads the clock; the pure decision core (decideAction) composes the
// booleans this returns. All timing flows through r.Now, so the schedule
// is deterministic under an injected clock (RFC §5.9).
//
//   - BudgetExhausted: the windowed crash count has reached CrashBudgetLimit
//     (5) within CrashBudgetWindow (10 min). A stale window (its `since`
//     older than the window) is treated as reset — a long-stable agent does
//     not carry an ancient crash count.
//   - BackoffElapsed: the exponential backoff (10s ×2 cap 300s) for the
//     UPCOMING restart (step = current crash count + 1) has elapsed since
//     the last observed exit (Status.LastExit.At, stamped when the terminal
//     was first observed).
//   - LivenessFailed: the heartbeat for a still-running container is staler
//     than LivenessFailureThreshold × LivenessPeriod — the windowed
//     equivalent of "3 consecutive failed probes" a single periodic read can
//     assert without holding per-probe state.
func (r *Reconciler) computeRecovery(def sextantproto.AgentDefinition, actual actualState) recoveryInputs {
	now := r.Now()
	status := def.Status

	var rec recoveryInputs

	// Crash budget. Only counts within the live window; a window whose
	// `since` predates the window has lapsed and does not count.
	windowLive := !status.CrashWindow.Since.IsZero() &&
		now.Sub(status.CrashWindow.Since.Time) < CrashBudgetWindow
	if windowLive && status.CrashWindow.Count >= CrashBudgetLimit {
		rec.BudgetExhausted = true
	}

	// Backoff. The step is the count of restarts already taken in this
	// window (so the FIRST restart waits backoffFor(1)=10s, the second
	// backoffFor(2)=20s, …). Anchored on the observed exit time.
	count := 0
	if windowLive {
		count = status.CrashWindow.Count
	}
	wait := backoffFor(count + 1)
	var exitAt time.Time
	if status.LastExit != nil {
		exitAt = status.LastExit.At.Time
	}
	if exitAt.IsZero() {
		// No recorded exit yet — the terminal was only just observed this
		// pass (e.g. actionMarkLost stamps LastExit on the way out). Hold
		// off until a later pass has the anchor.
		rec.BackoffElapsed = false
	} else {
		rec.BackoffElapsed = now.Sub(exitAt) >= wait
	}

	// Liveness probe (only meaningful while the container is observed
	// running). A heartbeat staler than LivenessPeriod is one failed probe;
	// the persisted LivenessFailures counter accumulates CONSECUTIVE
	// failures, and the THIRD (LivenessFailureThreshold) trips the restart
	// path for a wedged-but-running agent docker `die` never catches.
	if actual.ContainerRunning && r.Heartbeats != nil {
		rec.livenessProbedRunning = true
		last, ok := r.Heartbeats.LastSeen(def.UUID)
		// No heartbeat ever seen is NOT a failure here — a just-actuated
		// container has not had time to beat; the periodic sweep re-probes.
		if ok && now.Sub(last) >= LivenessPeriod {
			rec.livenessProbeFailed = true
			// Effective consecutive-failure count INCLUDING this pass.
			if status.LivenessFailures+1 >= LivenessFailureThreshold {
				rec.LivenessFailed = true
			}
		}
	}

	return rec
}

// backoffFor returns the exponential backoff wait for the nth restart
// (1-indexed) in a crash window: RecoveryBackoffInitial × Factor^(n-1),
// capped at RecoveryBackoffCap. n≤1 → initial (RFC §8: 10 → 20 → 40 → 80
// → 160 → 300).
func backoffFor(n int) time.Duration {
	if n <= 1 {
		return RecoveryBackoffInitial
	}
	wait := RecoveryBackoffInitial
	for i := 1; i < n; i++ {
		wait *= RecoveryBackoffFactor
		if wait >= RecoveryBackoffCap {
			return RecoveryBackoffCap
		}
	}
	return wait
}

// observe re-reads actual container reality for def (level-triggered).
// It resolves the live incarnation's container id from the incarnations
// bucket, lists docker by the incarnation-id label, and folds in the
// in-memory sidecar-terminal + die-debounce hint state.
func (r *Reconciler) observe(ctx context.Context, def sextantproto.AgentDefinition) (actualState, error) {
	var actual actualState

	incID := def.Status.CurrentIncarnationID

	// Sidecar-terminal precedence: if a terminal was observed for the
	// current incarnation, surface it so decideRun does not downgrade to
	// lost.
	if incID != uuid.Nil {
		r.mu.Lock()
		termState, terminal := r.sidecarTerminal[incID]
		dieT, hadDie := r.dieAt[incID]
		r.mu.Unlock()
		actual.SidecarTerminalObserved = terminal
		actual.SidecarTerminalState = termState

		// 5s die-debounce: within the window after an observed die, do not
		// let the reconciler infer lost yet — a clean sidecar `ended` may
		// still be in flight. We model this by reporting the container as
		// "present but not running" (decideRun → pending, give it a tick)
		// until the window elapses.
		if hadDie && r.Now().Sub(dieT) < r.DieDebounce {
			actual.ContainerPresent = true
			actual.ContainerRunning = false
			return actual, nil
		}
	}

	if r.Containers == nil || incID == uuid.Nil {
		return actual, nil
	}

	infos, err := r.Containers.List(ctx, containermgr.Filter{
		Labels: map[string]string{
			handlers.LabelAgentUUID:     def.UUID.String(),
			handlers.LabelIncarnationID: incID.String(),
		},
	})
	if err != nil {
		return actual, fmt.Errorf("list containers: %w", err)
	}
	var runningLabels map[string]string
	for _, info := range infos {
		// Scope to this host (multi-host safety, mirrors the old L2).
		if r.HostID != "" && info.Labels[handlers.LabelHostID] != r.HostID {
			continue
		}
		actual.ContainerPresent = true
		if info.Status == "running" {
			actual.ContainerRunning = true
			runningLabels = info.Labels
		}
	}

	// Drift (RFC §5.6, §5.8) is only actionable against a live RUNNING
	// container — a stale spec on an exited/lost container is converged by
	// the loss/recovery paths instead. Compute the verdict here so the pure
	// decision core stays IO-free.
	if actual.ContainerRunning {
		actual.Drift = r.computeDrift(ctx, def, incID, runningLabels)
	}
	return actual, nil
}

// computeDrift compares the RUNNING container's stamped spec-identity
// labels against the recomputed desired spec identity (RFC §5.6, §5.8) and
// folds in the turn-boundary gate. It is the imperative shell's drift
// sensor: it does the IO (recompute via the C0 builder) and the in-memory
// turn-state read, handing the pure decision core a verdict.
//
// FALSE-POSITIVE SAFETY: we diff OUR recomputed fingerprint (rebuilt from
// the persisted spec via the same buildAgentContainerSpec) against the
// label WE stamped — never docker's normalized/injected live spec. So
// docker-added mounts/env never read as drift. Every uncertainty
// (no fingerprinter wired, missing label, recompute error) fails SAFE:
// Drifted stays false and a healthy agent is left running.
func (r *Reconciler) computeDrift(ctx context.Context, def sextantproto.AgentDefinition, incID uuid.UUID, runningLabels map[string]string) driftInputs {
	var d driftInputs

	r.mu.Lock()
	d.AtTurnBoundary = r.atTurnBoundary[incID]
	r.mu.Unlock()

	if r.Actuator == nil || runningLabels == nil {
		return d
	}
	stampedFP, hasFP := runningLabels[handlers.LabelSpecFingerprint]
	if !hasFP || stampedFP == "" {
		// No fingerprint stamped (a container that predates C0). Can't make a
		// safe comparison — leave it converged rather than risk a false
		// restart. The epoch label is checked below independently.
		stampedFP = ""
	}

	desired, err := r.Actuator.DesiredFingerprint(ctx, def)
	if err != nil {
		// Recompute failed (e.g. transient host-state resolution error). Fail
		// safe — do not restart a healthy agent on a probe error; the next
		// sweep re-checks.
		log.Printf("sextantd: reconcile %s: drift recompute: %v", def.UUID, err)
		return d
	}

	// Fingerprint mismatch ⇒ the running container was built from a stale
	// spec (only assert it when both sides are present, so a missing stamp
	// never reads as drift).
	if stampedFP != "" && desired.Fingerprint != "" && stampedFP != desired.Fingerprint {
		d.Drifted = true
	}

	// Epoch skew (RFC §5.8): a container stamped with an OLDER wire-epoch
	// than the daemon's was built by a since-upgraded daemon ⇒ drift. A
	// missing/garbage label is treated as 0 (older than any real epoch),
	// which correctly flags pre-epoch containers as needing a rebuild onto
	// the current epoch. Only label < current is drift; never label > current
	// (a newer container the daemon hasn't caught up to — leave it be).
	if stampedEpoch, ok := parseEpochLabel(runningLabels[handlers.LabelWireEpoch]); !ok || stampedEpoch < desired.WireEpoch {
		d.Drifted = true
	}

	return d
}

// parseEpochLabel parses a stamped wire-epoch label. ok=false for an
// absent or non-integer value (treated by the caller as stale).
func parseEpochLabel(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// statusWrite bundles everything a single status update applies. It keeps
// the recovery bookkeeping (crash window, backoff anchor, liveness
// counter) readable rather than smearing positional args across
// writeObserved's signature.
type statusWrite struct {
	dec decision
	// rec is this pass's recovery verdict; it carries the raw liveness
	// probe outcome the bookkeeping persists into the consecutive-failure
	// counter.
	rec recoveryInputs
	// newIncarnation/gen/nonce are stamped on an actuation so the next
	// pass is converged. Zero newIncarnation means "no actuation."
	newIncarnation uuid.UUID
	gen            int
	nonce          int
}

// writeObserved is the SOLE-writer status update (RFC §5.2). It
// retry-rebases on CAS conflict (RFC §5.8: a background loop must not
// bail on 409). When dec.Observed is empty it leaves observed unchanged
// (e.g. a stop still draining). It also owns the P1 recovery bookkeeping:
// the monotonic RestartCount, the windowed CrashWindow budget, the
// exponential-backoff anchor (LastExit.At), the stable-run RunningSince,
// and the liveness counter (RFC §8).
//
// CRITICAL guardrail (RFC §5.2): a status-only write must NOT itself
// trigger a reconcile. This method does not Enqueue — the daemon wires
// the watch so status-only KV changes are filtered (see the daemon
// wiring + the no-self-reconcile test).
func (r *Reconciler) writeObserved(ctx context.Context, agentID uuid.UUID, sw statusWrite) error {
	dec := sw.dec
	nowT := r.Now().UTC()
	now := sextantproto.AtTimestamp(nowT)
	for attempt := 0; attempt < reconcileCASRetries; attempt++ {
		entry, err := r.Defs.Get(ctx, agentID.String())
		if err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				return nil
			}
			return fmt.Errorf("get for status write: %w", err)
		}
		var def sextantproto.AgentDefinition
		if err := json.Unmarshal(entry.Value(), &def); err != nil {
			return fmt.Errorf("decode for status write: %w", err)
		}

		before := def.Status
		prevObserved := def.Status.Observed
		def.Status.LastReconciledAt = now
		if dec.Observed != "" {
			def.Status.Observed = dec.Observed
			def.Status.Phase = string(dec.Observed)
		}

		// Recovery bookkeeping driven by the observed transition (RFC §8).
		r.applyRecoveryBookkeeping(&def.Status, sw, prevObserved, nowT, now)

		if sw.newIncarnation != uuid.Nil {
			def.Status.CurrentIncarnationID = sw.newIncarnation
			def.Status.ObservedGeneration = sw.gen
			def.Status.ObservedNonce = sw.nonce
			def.Status.RestartCount++
			// A fresh incarnation has not yet been observed running; reset the
			// per-incarnation liveness counter and the stable-run anchor.
			def.Status.LivenessFailures = 0
			def.Status.RunningSince = sextantproto.Timestamp{}
		}

		// Idempotence shortcut: nothing meaningful changed (only
		// LastReconciledAt would move) — skip the write so a steady-state
		// reconcile is a true no-op and does not churn the KV / version.
		if sw.newIncarnation == uuid.Nil && statusEqualIgnoringReconcileTime(before, def.Status) {
			return nil
		}

		def.Version++
		def.UpdatedAt = now
		raw, err := json.Marshal(def)
		if err != nil {
			return fmt.Errorf("marshal for status write: %w", err)
		}
		_, err = r.Defs.Update(ctx, agentID.String(), raw, entry.Revision())
		if err == nil {
			return nil
		}
		if !errors.Is(err, jetstream.ErrKeyExists) {
			return fmt.Errorf("update status: %w", err)
		}
		// CAS conflict — retry-rebase (a concurrent verb spec edit slipped
		// in). The reconciler is a background loop; it re-reads + re-applies
		// rather than surfacing the 409.
	}
	return fmt.Errorf("status write for %s: gave up after %d CAS conflicts", agentID, reconcileCASRetries)
}

// applyRecoveryBookkeeping mutates the recovery counters per the observed
// transition (RFC §8). It runs BEFORE the actuation stamp so a recovery
// restart sees the pre-restart crash window.
//
//   - Into a terminal (lost/crashed/ended) from a non-terminal: stamp
//     LastExit.At (the backoff anchor) and clear the stable-run anchor.
//   - A recovery restart (RecoveryRestart actuation): increment the
//     windowed crash count (opening the window if empty/lapsed).
//   - Into healthy running: set RunningSince on first sight, reset the
//     liveness counter, and reset the crash window once the run has been
//     stable ≥ RecoveryBackoffReset (an INDEPENDENT constant — RFC §8).
func (r *Reconciler) applyRecoveryBookkeeping(status *sextantproto.AgentStatusRecord, sw statusWrite, prevObserved sextantproto.ObservedState, nowT time.Time, now sextantproto.Timestamp) {
	dec := sw.dec
	newObserved := status.Observed

	// (a) First observation of a terminal — anchor the backoff on the exit
	// time so computeRecovery can measure the wait next pass.
	if newObserved.IsTerminal() && prevObserved != newObserved {
		status.LastExit = &sextantproto.LastExit{
			Reason: string(newObserved),
			At:     now,
		}
		status.RunningSince = sextantproto.Timestamp{}
		status.LivenessFailures = 0
	}

	// (b) A recovery restart spends one unit of crash budget. A deliberate
	// re-actuation (spec/nonce bump) does NOT — it is not a crash.
	if dec.Action == actionActuate && dec.RecoveryRestart {
		windowLive := !status.CrashWindow.Since.IsZero() &&
			nowT.Sub(status.CrashWindow.Since.Time) < CrashBudgetWindow
		if !windowLive {
			status.CrashWindow = sextantproto.CrashWindow{Since: now}
		}
		status.CrashWindow.Count++
	}

	// (c) Healthy running — track the stable-run anchor, accumulate the
	// liveness counter, and reset the crash window after a stable run.
	if newObserved == sextantproto.ObservedRunning {
		// Liveness counter: a failed probe this pass increments the
		// consecutive-failure count; a healthy probe (or no probe) resets it.
		// (When the decision is a liveness-driven restart, newObserved is
		// pending, not running, so we are not in this branch — the counter is
		// cleared by the actuation reset instead.)
		if sw.rec.livenessProbedRunning && sw.rec.livenessProbeFailed {
			status.LivenessFailures++
		} else {
			status.LivenessFailures = 0
		}
		if status.RunningSince.IsZero() {
			status.RunningSince = now
		} else if nowT.Sub(status.RunningSince.Time) >= RecoveryBackoffReset {
			// Stable for the full reset window — clear the crash budget so a
			// later transient crash starts fresh (RFC §8: reset only after a
			// stable run; an INDEPENDENT constant, not 2×cap).
			status.CrashWindow = sextantproto.CrashWindow{}
		}
	}
}

// statusEqualIgnoringReconcileTime compares two status records ignoring
// LastReconciledAt (which moves every pass and must not count as a
// change, or every steady-state reconcile would churn the record and the
// idempotence oracle would fail).
func statusEqualIgnoringReconcileTime(a, b sextantproto.AgentStatusRecord) bool {
	a.LastReconciledAt = sextantproto.Timestamp{}
	b.LastReconciledAt = sextantproto.Timestamp{}
	return a == b
}
