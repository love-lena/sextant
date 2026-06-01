package sextantd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
type reconcileActuator interface {
	Actuate(ctx context.Context, def sextantproto.AgentDefinition, resumeSession bool) (handlers.ActuateResult, error)
	Stop(ctx context.Context, def sextantproto.AgentDefinition) error
	Teardown(ctx context.Context, def sextantproto.AgentDefinition) error
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
// enqueues. Non-terminal transitions (started/resumed) just enqueue so
// the reconciler converges observed=running.
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
	r.Enqueue(p.AgentUUID)
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
	if err := r.reconcileOne(pctx, agentID); err != nil {
		log.Printf("sextantd: reconcile %s: %v", agentID, err)
	}
}

// reconcileOne is the testable single-pass reconcile. Exported-ish for
// the package's own tests; not part of the public API.
func (r *Reconciler) reconcileOne(ctx context.Context, agentID uuid.UUID) error {
	entry, err := r.Defs.Get(ctx, agentID.String())
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil // agent purged; nothing to converge
		}
		return fmt.Errorf("get: %w", err)
	}
	var def sextantproto.AgentDefinition
	if err := json.Unmarshal(entry.Value(), &def); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	actual, err := r.observe(ctx, def)
	if err != nil {
		return fmt.Errorf("observe: %w", err)
	}

	dec := decideAction(def, actual)

	switch dec.Action {
	case actionNone:
		// Converge observed status only if the decision says so (e.g.
		// container is up but status still says pending → running).
		return r.writeObserved(ctx, agentID, dec, uuid.Nil, 0, 0)

	case actionActuate:
		res, aerr := r.Actuator.Actuate(ctx, def, resumeSessionFor(def))
		if aerr != nil {
			return fmt.Errorf("actuate: %w", aerr)
		}
		// Stamp the fresh incarnation + caught-up generation/nonce so the
		// next pass is converged (idempotence). Clear stale precedence
		// flags for the (now superseded) old incarnation.
		return r.writeObserved(ctx, agentID, dec, res.IncarnationID, def.Spec.Generation, def.Spec.ReactuationNonce)

	case actionStop:
		if serr := r.Actuator.Stop(ctx, def); serr != nil {
			return fmt.Errorf("stop: %w", serr)
		}
		return r.writeObserved(ctx, agentID, dec, uuid.Nil, 0, 0)

	case actionTeardown:
		if terr := r.Actuator.Teardown(ctx, def); terr != nil {
			return fmt.Errorf("teardown: %w", terr)
		}
		return r.writeObserved(ctx, agentID, dec, uuid.Nil, 0, 0)

	case actionMarkLost:
		return r.writeObserved(ctx, agentID, dec, uuid.Nil, 0, 0)

	default:
		return nil
	}
}

// resumeSessionFor decides whether the actuation should resume the SDK
// session. The restart handler clears Spec.Runtime.SessionID for a fresh
// session and keeps it for a preserved one, so "session id present" is
// the resume signal.
func resumeSessionFor(def sextantproto.AgentDefinition) bool {
	return def.Spec.Runtime.SessionID != nil
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
	for _, info := range infos {
		// Scope to this host (multi-host safety, mirrors the old L2).
		if r.HostID != "" && info.Labels[handlers.LabelHostID] != r.HostID {
			continue
		}
		actual.ContainerPresent = true
		if info.Status == "running" {
			actual.ContainerRunning = true
		}
	}
	return actual, nil
}

// writeObserved is the SOLE-writer status update (RFC §5.2). It
// retry-rebases on CAS conflict (RFC §5.8: a background loop must not
// bail on 409). When dec.Observed is empty it leaves observed unchanged
// (e.g. a stop still draining). newIncarnation/gen/nonce are stamped on
// an actuation so the next pass is converged.
//
// CRITICAL guardrail (RFC §5.2): a status-only write must NOT itself
// trigger a reconcile. This method does not Enqueue — the daemon wires
// the watch so status-only KV changes are filtered (see the daemon
// wiring + the no-self-reconcile test).
func (r *Reconciler) writeObserved(ctx context.Context, agentID uuid.UUID, dec decision, newIncarnation uuid.UUID, gen, nonce int) error {
	now := sextantproto.AtTimestamp(r.Now().UTC())
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
		def.Status.LastReconciledAt = now
		if dec.Observed != "" {
			def.Status.Observed = dec.Observed
			def.Status.Phase = string(dec.Observed)
		}
		if newIncarnation != uuid.Nil {
			def.Status.CurrentIncarnationID = newIncarnation
			def.Status.ObservedGeneration = gen
			def.Status.ObservedNonce = nonce
			def.Status.RestartCount++
		}

		// Idempotence shortcut: nothing meaningful changed (only
		// LastReconciledAt would move) — skip the write so a steady-state
		// reconcile is a true no-op and does not churn the KV / version.
		if newIncarnation == uuid.Nil && statusEqualIgnoringReconcileTime(before, def.Status) {
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

// statusEqualIgnoringReconcileTime compares two status records ignoring
// LastReconciledAt (which moves every pass and must not count as a
// change, or every steady-state reconcile would churn the record and the
// idempotence oracle would fail).
func statusEqualIgnoringReconcileTime(a, b sextantproto.AgentStatusRecord) bool {
	a.LastReconciledAt = sextantproto.Timestamp{}
	b.LastReconciledAt = sextantproto.Timestamp{}
	return a == b
}
