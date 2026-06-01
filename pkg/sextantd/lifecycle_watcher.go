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
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// AgentDefinitionsBucket names the NATS KV bucket the watcher writes
// into. Mirrors pkg/natsboot/layout.go's row and the same constant in
// pkg/rpc/handlers — declared here too so the watcher package doesn't
// import handlers (consumer-side interfaces only).
const AgentDefinitionsBucket = "agent_definitions"

// lifecycleSubject is the wildcard NATS subject the watcher subscribes
// to. The sidecar publishes one envelope per agent-lifecycle transition
// on `agents.<uuid>.lifecycle`; the watcher pulls the wildcard so it
// observes every agent without needing per-agent subscriptions.
const lifecycleSubject = "agents.*.lifecycle"

// watcherUpdateTimeout caps how long the per-message handler will wait
// when calling Get / Put on the definitions KV. The handler runs on the
// NATS client's dispatcher goroutine; we don't want a wedged KV write
// to block other messages indefinitely.
const watcherUpdateTimeout = 5 * time.Second

// MapLifecycleTransition projects a wire-level LifecycleEvent (the
// sidecar's "transition" field) into the LifecycleState we store on the
// AgentDefinition. The second return is false for transitions that
// don't change agent-level state — `turn_ended` (per-turn signal) and
// any unknown / forward-compat values.
//
// Mapping:
//
//	started   → running
//	resumed   → running
//	restarted → running
//	ended     → ended
//	crashed   → crashed
//	paused    → paused
//	archived  → archived
//	lost      → lost
//	turn_ended (and everything else) → no-op
//
// Exported so callers (and tests) can pin the mapping without re-reading
// the watcher's switch statement.
func MapLifecycleTransition(t sextantproto.LifecycleEvent) (sextantproto.LifecycleState, bool) {
	switch t {
	case sextantproto.LifecycleStarted,
		sextantproto.LifecycleResumedEvent,
		sextantproto.LifecycleRestartedEvent:
		return sextantproto.LifecycleRunning, true
	case sextantproto.LifecycleEnded:
		return sextantproto.LifecycleEndedState, true
	case sextantproto.LifecycleCrashedEvent:
		return sextantproto.LifecycleCrashedState, true
	case sextantproto.LifecyclePausedEvent:
		return sextantproto.LifecyclePaused, true
	case sextantproto.LifecycleArchivedEvent:
		return sextantproto.LifecycleArchived, true
	case sextantproto.LifecycleLostEvent:
		return sextantproto.LifecycleLostState, true
	default:
		// LifecycleTurnEnded and any unknown future event.
		return "", false
	}
}

// LifecycleDefinitionsKV is the minimal KV surface the watcher needs.
// Get + Update on the agent_definitions bucket. Update is used (not
// Put) so concurrent archive_agent / restart_agent / kill_agent writes
// cannot be clobbered by a stale sidecar lifecycle envelope — the
// revision check serializes us with them. See the type comment on
// LifecycleWatcher for the full race story.
type LifecycleDefinitionsKV interface {
	Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error)
	// Update writes value when the last-seen revision matches. Returns
	// a non-nil error (commonly jetstream.ErrKeyExists, depending on
	// the underlying KV) when the revision is stale.
	Update(ctx context.Context, key string, value []byte, revision uint64) (uint64, error)
}

// watcherCASRetries caps how many times applyTransition retries on a
// revision conflict. Each retry re-reads the record + re-evaluates the
// guards, so a runaway loop is impossible — 3 is generous.
const watcherCASRetries = 3

// LifecycleWatcher subscribes to `agents.*.lifecycle` and updates the
// matching AgentDefinition's Lifecycle field on every transition
// envelope. It exists because the spawn/restart/kill handlers only set
// lifecycle at request time; the sidecar's own `ended` / `crashed`
// signals were previously not observed by the daemon, leaving operators
// staring at `lifecycle: running` on dead agents.
//
// See slug:bug-agents-list-stale-lifecycle.
//
// The watcher uses a core NATS subscription (not JetStream) — we want
// at-most-once delivery of the latest envelope, not a durable replay.
// Lifecycle envelopes also flow into the JetStream `agent_lifecycle`
// stream where the shipper consumes them; the watcher is a separate
// hot-path reader.
//
// Concurrency: the handler runs on the NATS dispatcher goroutine. Each
// callback does a Get → mutate → Put against the KV; concurrent updates
// to the same key from spawn/restart/kill handlers can race the watcher
// here, but the worst case is a single-write overwrite — the next
// lifecycle envelope (or the next spawn/restart) re-converges the
// record. The KV's monotonic Version field makes the race observable
// in agent_definitions_history (post-M16) when we wire one in.
type LifecycleWatcher struct {
	nc   *nats.Conn
	defs LifecycleDefinitionsKV

	mu        sync.Mutex
	sub       *nats.Subscription
	observers []LifecycleObserver
}

// NewLifecycleWatcher subscribes to `agents.*.lifecycle` and returns a
// running watcher. Caller is responsible for calling Stop() during
// shutdown — the watcher does not own the *nats.Conn, only its
// subscription.
//
// Returns an error if nc or defs is nil, or if the subscription fails.
// On error the watcher is not started; caller doesn't need to call Stop.
func NewLifecycleWatcher(nc *nats.Conn, defs LifecycleDefinitionsKV) (*LifecycleWatcher, error) {
	if nc == nil {
		return nil, fmt.Errorf("lifecycle watcher: nats conn is nil")
	}
	if defs == nil {
		return nil, fmt.Errorf("lifecycle watcher: definitions KV is nil")
	}
	w := &LifecycleWatcher{
		nc:   nc,
		defs: defs,
	}
	sub, err := nc.Subscribe(lifecycleSubject, w.handle)
	if err != nil {
		return nil, fmt.Errorf("lifecycle watcher: subscribe %s: %w", lifecycleSubject, err)
	}
	w.mu.Lock()
	w.sub = sub
	w.mu.Unlock()
	return w, nil
}

// Stop unsubscribes from the lifecycle subject. Idempotent; safe to
// call after a fatal error returned from NewLifecycleWatcher (in which
// case it's a no-op).
func (w *LifecycleWatcher) Stop() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	sub := w.sub
	w.sub = nil
	w.mu.Unlock()
	if sub == nil {
		return nil
	}
	if err := sub.Unsubscribe(); err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
		return fmt.Errorf("lifecycle watcher: unsubscribe: %w", err)
	}
	return nil
}

// handle is the per-message NATS callback. It decodes the envelope,
// resolves the AgentDefinition, and (if the transition maps to a
// state-changing transition) writes the new Lifecycle back to KV.
//
// Errors are logged, not propagated — the NATS dispatcher has nowhere
// to send them. Operators trace these via the daemon log; a structural
// fix is to emit a `audit.watcher.lifecycle_update` envelope from here,
// tracked separately.
func (w *LifecycleWatcher) handle(msg *nats.Msg) {
	var env sextantproto.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		log.Printf("sextantd: lifecycle watcher: decode envelope on %s: %v",
			msg.Subject, err)
		return
	}
	if env.Kind != sextantproto.KindLifecycle {
		// Wrong kind on the lifecycle subject — shouldn't happen, but
		// drop quietly so a misrouted publisher doesn't spam the log.
		return
	}
	var payload sextantproto.LifecyclePayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		log.Printf("sextantd: lifecycle watcher: decode payload on %s: %v",
			msg.Subject, err)
		return
	}
	state, applies := MapLifecycleTransition(payload.Transition)
	if !applies {
		// turn_ended or an unknown future transition. No-op.
		return
	}
	if err := w.applyTransition(payload.AgentUUID.String(), payload.IncarnationID, state); err != nil {
		log.Printf("sextantd: lifecycle watcher: apply %s/%s: %v",
			payload.AgentUUID, payload.Transition, err)
		return
	}
	w.fireObservers(payload)
}

// watcherShouldDropForIncarnation reports whether the watcher should
// drop a lifecycle envelope based on the incarnation match. Returns
// true (drop) when the def carries a known CurrentIncarnationID AND
// it differs from the envelope's IncarnationID. Returns false (apply)
// when:
//
//   - def.CurrentIncarnationID is zero (uuid.Nil) — pre-incarnation-
//     field installs OR agents whose def was last touched before
//     spawn/restart populated the field. Warm-up: trust the bus.
//   - envelope IncarnationID matches def.CurrentIncarnationID.
//   - envelope IncarnationID is zero — shouldn't happen on the bus,
//     but we don't block on the case.
func watcherShouldDropForIncarnation(current, envelope uuid.UUID) bool {
	if current == uuid.Nil {
		return false
	}
	if envelope == uuid.Nil {
		return false
	}
	return current != envelope
}

// applyTransition reads the current AgentDefinition, decides whether
// the watcher's new state should overwrite it, and writes back via
// CAS so concurrent archive/restart/kill writes cannot be clobbered
// by a stale sidecar lifecycle envelope. Returns nil on:
//
//   - unknown agent (the watcher does not repopulate purged records)
//   - stale incarnation (envelope from a non-current incarnation —
//     gated on def.CurrentIncarnationID, the authoritative anchor
//     spawn/restart set before the new sidecar's `started` reaches
//     the bus)
//   - idempotent target (current state already matches)
//   - terminal-priority guard (current state is archived; the
//     operator's explicit terminal — see watcherShouldYield)
//   - CAS conflict that the retry loop resolved
//
// Returns the wrapped error on:
//
//   - decode failure
//   - persistent CAS conflict (>watcherCASRetries collisions)
//   - any other KV error
func (w *LifecycleWatcher) applyTransition(key string, envelopeIncarnation uuid.UUID, state sextantproto.LifecycleState) error {
	ctx, cancel := context.WithTimeout(context.Background(), watcherUpdateTimeout)
	defer cancel()

	for attempt := 0; attempt < watcherCASRetries; attempt++ {
		entry, err := w.defs.Get(ctx, key)
		if err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				// Unknown agent. The watcher refuses to create records from
				// thin air — a forged envelope or a stale subject from a
				// purged agent must not repopulate the bucket.
				return nil
			}
			return fmt.Errorf("get %s: %w", key, err)
		}
		var def sextantproto.AgentDefinition
		if err := json.Unmarshal(entry.Value(), &def); err != nil {
			return fmt.Errorf("decode %s: %w", key, err)
		}
		if watcherShouldDropForIncarnation(def.CurrentIncarnationID, envelopeIncarnation) {
			// Stale envelope from a non-current incarnation. The most
			// common case is a restart_agent's prior incarnation
			// publishing its delayed terminal envelope after the
			// daemon swapped to a fresh incarnation. spawn / restart
			// set def.CurrentIncarnationID before the new sidecar's
			// `started` reaches the bus, so we can drop with confidence
			// here instead of trusting bus-derived in-memory state.
			log.Printf("sextantd: lifecycle watcher: drop stale envelope for agent=%s envelope_incarnation=%s current_incarnation=%s",
				key, envelopeIncarnation, def.CurrentIncarnationID)
			return nil
		}
		if def.Lifecycle == state {
			// No change — skip the write so spawn/restart's
			// version-bumps aren't churned by an idempotent lifecycle
			// envelope.
			return nil
		}
		if watcherShouldYield(def.Lifecycle, state) {
			// Two cases:
			//   1. Current is archived (operator-explicit terminal).
			//      A stale sidecar "ended" arriving after archive
			//      would otherwise release the name-uniqueness lock.
			//   2. Current is ended/crashed and the proposed state is
			//      `lost`. The sidecar already observed the cause;
			//      daemon-inferred absence must not clobber that.
			// See watcherShouldYield for the rule.
			return nil
		}
		def.Lifecycle = state
		def.Version++
		def.UpdatedAt = sextantproto.NowTimestamp()
		raw, err := json.Marshal(def)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		_, err = w.defs.Update(ctx, key, raw, entry.Revision())
		if err == nil {
			return nil
		}
		if !isCASConflict(err) {
			return fmt.Errorf("update %s: %w", key, err)
		}
		// CAS conflict — concurrent archive/restart/kill wrote the
		// record between our Get and Update. Re-read and re-evaluate
		// the guards; the new revision may now be archived (terminal)
		// in which case the next iteration's watcherShouldYield
		// returns and we drop the envelope.
	}
	return fmt.Errorf("update %s: gave up after %d CAS conflicts", key, watcherCASRetries)
}

// watcherShouldYield reports whether the watcher must NOT overwrite
// the current lifecycle with the proposed new state. Two rules:
//
//  1. Operator-explicit `archived` outranks every sidecar-driven or
//     daemon-inferred transition (existing rule).
//  2. Sidecar-observed terminals (`ended`, `crashed`) outrank
//     daemon-inferred `lost` — observed cause beats inferred absence.
func watcherShouldYield(current, proposed sextantproto.LifecycleState) bool {
	if current == sextantproto.LifecycleArchived {
		return true
	}
	if proposed == sextantproto.LifecycleLostState &&
		(current == sextantproto.LifecycleEndedState ||
			current == sextantproto.LifecycleCrashedState) {
		return true
	}
	return false
}

// isCASConflict reports whether the given error indicates that the
// KV's Update rejected our revision as stale. nats.go's jetstream
// returns ErrKeyExists for this — keeping the predicate inline so
// the call site doesn't import that error directly.
func isCASConflict(err error) bool {
	return errors.Is(err, jetstream.ErrKeyExists)
}

// LifecycleObserver is called after the watcher successfully applies
// a lifecycle envelope to the KV. Observers run synchronously on the
// dispatcher goroutine; keep them fast.
type LifecycleObserver func(sextantproto.LifecyclePayload)

// RegisterLifecycleObserver appends an observer. Call before the watcher
// starts receiving traffic (typically immediately after NewLifecycleWatcher).
// Not safe to call concurrently with handle().
func RegisterLifecycleObserver(w *LifecycleWatcher, o LifecycleObserver) {
	if w == nil || o == nil {
		return
	}
	w.mu.Lock()
	w.observers = append(w.observers, o)
	w.mu.Unlock()
}

// fireObservers calls each registered observer with the successfully-applied
// payload. Observers are copied under the lock so new registrations during
// dispatch cannot race.
func (w *LifecycleWatcher) fireObservers(p sextantproto.LifecyclePayload) {
	w.mu.Lock()
	obs := append([]LifecycleObserver(nil), w.observers...) // copy under lock
	w.mu.Unlock()
	for _, o := range obs {
		o(p)
	}
}
