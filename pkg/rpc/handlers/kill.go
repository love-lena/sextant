package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// KillDeps bundles the dependencies the kill handler needs. Under the
// declarative model kill_agent is a desired-state edit (spec.desired =
// paused); it no longer stops the container itself — the reconciler does.
//
// Containers is retained on the struct so the daemon can build one dep
// bag, but the handler does not call it.
type KillDeps struct {
	Definitions  AgentMutableKV
	Incarnations AgentMutableKV
	Containers   ContainerRunner
	// Enqueue hints the reconciler to converge the paused intent now.
	Enqueue ReconcileEnqueuer
	Now     func() time.Time
}

// killCASRetries caps how many times the desired-state write retries on
// a CAS conflict. The reconciler writes status concurrently, so a verb's
// spec edit composes with those background writes via re-read + re-apply
// rather than bailing on the first collision.
const killCASRetries = 3

// NewKillAgent returns a Handler for the kill_agent verb (stop). Under
// the declarative model this writes spec.desired=paused and enqueues a
// reconcile — the reconciler stops the container (sole actuator). The
// verb NAME stays kill_agent (a separate pending rename); its semantics
// are "stop, retain the record."
//
//  1. Decode args; resolve the def.
//  2. CAS spec.desired = paused (retry on concurrent status writes).
//  3. Enqueue a reconcile; reply {ok: true}.
func NewKillAgent(deps KillDeps) rpc.Handler {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		var args sextantproto.KillAgentRequest
		if err := json.Unmarshal(req.Payload, &args); err != nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest,
				fmt.Sprintf("decode kill_agent payload: %v", err))
		}
		if args.AgentID == uuid.Nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest, "agent_id is required")
		}

		defEntry, err := deps.Definitions.Get(ctx, args.AgentID.String())
		if err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				return emitErr(emit, sextantproto.ErrCodeAgentNotFound,
					fmt.Sprintf("no agent with uuid %s", args.AgentID))
			}
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("load definition: %v", err))
		}

		if err := setDesired(ctx, deps.Definitions, args.AgentID, defEntry, sextantproto.DesiredPaused, deps.Now); err != nil {
			if errors.Is(err, errDesiredCASExhausted) {
				return emitErr(emit, sextantproto.ErrCodeBadRequest,
					fmt.Sprintf("agent %s definition changed during stop (concurrent restart/archive); re-issue if still appropriate", args.AgentID))
			}
			return emitErr(emit, sextantproto.ErrCodeInternal, err.Error())
		}
		if deps.Enqueue != nil {
			deps.Enqueue.Enqueue(args.AgentID)
		}
		return emitOK(emit, sextantproto.KillAgentResponse{OK: true})
	}
}

// errDesiredCASExhausted signals the desired-state edit ran out of CAS
// retries. Resolved via errors.Is at the caller so error mapping stays
// in one place.
var errDesiredCASExhausted = errors.New("desired-state edit: gave up after CAS retries")

// setDesired CAS-writes def.Spec.Desired = desired, bumping Version +
// UpdatedAt, retrying on a concurrent (reconciler status) write by
// re-reading + re-applying. firstEntry pins the revision read for the
// no-conflict fast path. It is the shared desired-state-edit primitive
// for kill / archive.
//
// Carries forward the incarnation-CAS discipline at the verb layer: the
// CAS revision check is what stops a stale verb write from clobbering a
// concurrent reconciler status update.
func setDesired(ctx context.Context, kv AgentMutableKV, agentID uuid.UUID, firstEntry jetstream.KeyValueEntry, desired sextantproto.DesiredState, nowFn func() time.Time) error {
	if nowFn == nil {
		nowFn = time.Now
	}
	entry := firstEntry
	for attempt := 0; attempt < killCASRetries; attempt++ {
		if attempt > 0 {
			fresh, err := kv.Get(ctx, agentID.String())
			if err != nil {
				return fmt.Errorf("re-read definition: %w", err)
			}
			entry = fresh
		}
		var def sextantproto.AgentDefinition
		if err := json.Unmarshal(entry.Value(), &def); err != nil {
			return fmt.Errorf("decode definition: %w", err)
		}
		if def.Spec.Desired == desired {
			// Idempotent: already at the requested intent.
			return nil
		}
		def.Spec.Desired = desired
		def.Spec.Generation++
		def.Version++
		def.UpdatedAt = sextantproto.AtTimestamp(nowFn().UTC())
		raw, err := json.Marshal(def)
		if err != nil {
			return fmt.Errorf("marshal definition: %w", err)
		}
		_, err = kv.Update(ctx, agentID.String(), raw, entry.Revision())
		if err == nil {
			return nil
		}
		if !errors.Is(err, jetstream.ErrKeyExists) {
			return fmt.Errorf("update definition: %w", err)
		}
		// CAS conflict — loop, re-read, re-apply.
	}
	return errDesiredCASExhausted
}

// findLiveIncarnation walks the incarnations bucket and returns the
// (incarnation, key) pair whose AgentUUID matches and whose EndedAt is
// nil. Returns (nil, "", nil) if there isn't one. Used by the actuator
// to find the prior incarnation to stop and by the reconciler to
// re-observe container reality.
func findLiveIncarnation(ctx context.Context, kv AgentMutableKV, agent uuid.UUID) (*sextantproto.AgentIncarnation, string, error) {
	lister, err := kv.ListKeys(ctx)
	if err != nil {
		if errors.Is(err, jetstream.ErrNoKeysFound) || errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("list keys: %w", err)
	}
	defer func() { _ = lister.Stop() }()
	for key := range lister.Keys() {
		entry, err := kv.Get(ctx, key)
		if err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				continue
			}
			return nil, "", fmt.Errorf("get %s: %w", key, err)
		}
		var inc sextantproto.AgentIncarnation
		if err := json.Unmarshal(entry.Value(), &inc); err != nil {
			continue
		}
		if inc.AgentUUID != agent {
			continue
		}
		if inc.EndedAt != nil || inc.State == sextantproto.IncarnationExited || inc.State == sextantproto.IncarnationFailed {
			continue
		}
		incCopy := inc
		return &incCopy, key, nil
	}
	return nil, "", nil
}

// rpc.Handler is the return type of NewKillAgent; keep this import alive.
var _ = rpc.VerbKillAgent
