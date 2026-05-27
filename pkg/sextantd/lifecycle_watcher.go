package sextantd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

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
	default:
		// LifecycleTurnEnded and any unknown future event.
		return "", false
	}
}

// LifecycleDefinitionsKV is the minimal KV surface the watcher needs.
// Get + Put on the agent_definitions bucket; no list, no watch (the
// watcher is reading the subject, not the KV). Defined here on the
// consumer side so this package doesn't depend on the full handlers KV
// abstraction.
type LifecycleDefinitionsKV interface {
	Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error)
	Put(ctx context.Context, key string, value []byte) (uint64, error)
}

// LifecycleWatcher subscribes to `agents.*.lifecycle` and updates the
// matching AgentDefinition's Lifecycle field on every transition
// envelope. It exists because the spawn/restart/kill handlers only set
// lifecycle at request time; the sidecar's own `ended` / `crashed`
// signals were previously not observed by the daemon, leaving operators
// staring at `lifecycle: running` on dead agents.
//
// See plans/issues/bug-agents-list-stale-lifecycle.md.
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

	mu  sync.Mutex
	sub *nats.Subscription
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
	if err := w.applyTransition(payload.AgentUUID.String(), state); err != nil {
		log.Printf("sextantd: lifecycle watcher: apply %s/%s: %v",
			payload.AgentUUID, payload.Transition, err)
	}
}

// applyTransition reads the current AgentDefinition, rewrites the
// Lifecycle / Version / UpdatedAt fields, and writes back. Returns
// jetstream.ErrKeyNotFound (wrapped) if the agent is unknown — the
// watcher drops those, see handle().
func (w *LifecycleWatcher) applyTransition(key string, state sextantproto.LifecycleState) error {
	ctx, cancel := context.WithTimeout(context.Background(), watcherUpdateTimeout)
	defer cancel()
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
	if def.Lifecycle == state {
		// No change — skip the write so spawn/restart's version-bumps
		// aren't churned by an idempotent lifecycle envelope.
		return nil
	}
	def.Lifecycle = state
	def.Version++
	def.UpdatedAt = sextantproto.NowTimestamp()
	raw, err := json.Marshal(def)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if _, err := w.defs.Put(ctx, key, raw); err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	return nil
}
