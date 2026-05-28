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

// KillDeps bundles the dependencies the kill handler needs.
type KillDeps struct {
	Definitions  AgentMutableKV
	Incarnations AgentMutableKV
	Containers   ContainerRunner
	Now          func() time.Time
}

// killCASRetries caps how many times the final definition commit retries
// on a CAS conflict before returning BAD_REQUEST. Mirrors
// lifecycle_watcher.go's watcherCASRetries (3) — the daemon's own
// LifecycleWatcher / Reconciler perform legitimate read-modify-write
// against the same def, so the retry budget composes kill's intent with
// those background writers instead of bailing on every concurrent
// daemon-side commit.
//
// Asymmetry vs restart_agent / archive_agent (which BAIL with rollback):
// kill's container-stop side effect is idempotent — a stopped container
// can be stopped again as a no-op — so retrying the def-write after the
// stop already ran is safe. restart_agent's side effect (spawn a new
// container) is NOT idempotent, hence its bail-with-rollback shape. See
// plans/issues/bug-kill-agent-cas-flakes-integration-tests.md.
const killCASRetries = 3

// NewKillAgent returns a Handler for the kill_agent verb. Flow:
//
//  1. Decode args.
//  2. Look up the AgentDefinition; error 404 if missing.
//  3. Find the live AgentIncarnation (state == starting|ready, ended_at nil).
//  4. containermgr.Stop with the per-request grace.
//  5. Update the incarnation's State, ExitCode, EndedAt.
//  6. Flip the definition's lifecycle to "defined" (back to dormant —
//     archive is a separate verb).
//  7. Reply {ok: true}.
//
// Multiple incarnations per agent at the same time are not legal per
// architecture §2, so we stop at the first live one we find.
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
		grace := time.Duration(args.GraceSeconds) * time.Second
		if grace <= 0 {
			grace = defaultGraceSeconds * time.Second
		}

		// 2. Load definition. The entry is passed into commitKillFlip
		// as the first-iteration revision so the no-conflict path
		// makes one fewer KV round-trip; commitKillFlip handles the
		// decode + retry on CAS conflict.
		defEntry, err := deps.Definitions.Get(ctx, args.AgentID.String())
		if err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				return emitErr(emit, sextantproto.ErrCodeAgentNotFound,
					fmt.Sprintf("no agent with uuid %s", args.AgentID))
			}
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("load definition: %v", err))
		}

		// 3. Find live incarnation.
		inc, incKey, err := findLiveIncarnation(ctx, deps.Incarnations, args.AgentID)
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("find live incarnation: %v", err))
		}
		if inc == nil {
			// No live incarnation — fall through to lifecycle flip so a
			// caller can use kill_agent to put a stuck agent back to
			// defined without errors. Retry the CAS write on conflict
			// so concurrent legitimate daemon writes (LifecycleWatcher
			// / Reconciler) compose with kill's intent instead of
			// bailing — same retry budget as the post-stop path.
			if err := commitKillFlip(ctx, deps, args.AgentID, defEntry); err != nil {
				return emitKillCASErr(emit, args.AgentID, err, false)
			}
			return emitOK(emit, sextantproto.KillAgentResponse{OK: true})
		}

		// 4. Stop the container. CRITICAL: side effects run ONCE,
		// before the CAS retry loop. The retry below replays only the
		// def-write, never the stop — kill's stop is idempotent in
		// practice (a stopped container can be stopped again as a
		// no-op), but more importantly the retry exists for concurrent
		// daemon writers, not for replaying the original kill intent.
		if inc.ContainerID != "" {
			if err := deps.Containers.Stop(ctx, inc.ContainerID, grace); err != nil {
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("stop container %s: %v", inc.ContainerID, err))
			}
		}

		// 5. Update the incarnation.
		ended := sextantproto.AtTimestamp(deps.Now().UTC())
		inc.State = sextantproto.IncarnationExited
		inc.EndedAt = &ended
		if err := putJSON(ctx, deps.Incarnations, incKey, *inc); err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("update incarnation: %v", err))
		}

		// 6. Flip the definition back to defined via CAS, retrying on
		// concurrent legitimate writes (LifecycleWatcher updating
		// lifecycle in response to the sidecar's `ended` envelope, the
		// reconciler converging def state, etc.). Each retry re-reads
		// the def + re-applies the mutation, so we compose with the
		// concurrent writer's commit instead of stomping it. On budget
		// exhaustion or a genuine operator-action-in-flight (the next
		// kill / restart / archive on a fast-typing operator), the
		// loop gives up and the operator sees BAD_REQUEST + re-issues.
		// The container is already stopped at this point — note in
		// the error message so the operator knows the side effect
		// landed even on bail.
		if err := commitKillFlip(ctx, deps, args.AgentID, defEntry); err != nil {
			return emitKillCASErr(emit, args.AgentID, err, true)
		}
		return emitOK(emit, sextantproto.KillAgentResponse{OK: true})
	}
}

// commitKillFlip runs the def-side mutation of kill_agent (flip
// lifecycle to defined, bump version, refresh UpdatedAt) under a CAS
// retry budget of killCASRetries. Each iteration re-reads the def to
// pick up any concurrent legitimate writes (e.g. the LifecycleWatcher
// updating lifecycle in response to the sidecar's `ended` envelope) and
// re-applies the mutation on top of them. firstEntry pins the def
// revision the caller read for step 2 / 3; the first iteration reuses
// it so a no-conflict path makes exactly one extra KV round-trip — the
// Update — instead of an extra Get.
//
// Returns nil on success. Returns ErrKillCASExhausted when the budget
// runs out (caller maps to BAD_REQUEST). All other errors are wrapped
// with %w from the underlying Get / Update / Marshal.
func commitKillFlip(
	ctx context.Context,
	deps KillDeps,
	agentID uuid.UUID,
	firstEntry jetstream.KeyValueEntry,
) error {
	entry := firstEntry
	for attempt := 0; attempt < killCASRetries; attempt++ {
		if attempt > 0 {
			fresh, err := deps.Definitions.Get(ctx, agentID.String())
			if err != nil {
				return fmt.Errorf("re-read definition: %w", err)
			}
			entry = fresh
		}
		var def sextantproto.AgentDefinition
		if err := json.Unmarshal(entry.Value(), &def); err != nil {
			return fmt.Errorf("decode definition: %w", err)
		}
		def.Lifecycle = sextantproto.LifecycleDefined
		def.Version++
		def.UpdatedAt = sextantproto.AtTimestamp(deps.Now().UTC())
		raw, err := json.Marshal(def)
		if err != nil {
			return fmt.Errorf("marshal definition: %w", err)
		}
		_, err = deps.Definitions.Update(ctx, agentID.String(), raw, entry.Revision())
		if err == nil {
			return nil
		}
		if !errors.Is(err, jetstream.ErrKeyExists) {
			return fmt.Errorf("update definition: %w", err)
		}
		// CAS conflict — loop, re-read, re-apply on the next iteration.
	}
	return errKillCASExhausted
}

// errKillCASExhausted signals that commitKillFlip ran out of retries.
// Kept unexported and resolved via errors.Is at the caller so the
// error-mapping logic stays in one place.
var errKillCASExhausted = errors.New("kill_agent: gave up after CAS retries")

// emitKillCASErr maps commitKillFlip's error to the appropriate RPC
// error envelope. containerStopped governs the wording: callers from
// the post-stop path get the "container was stopped" suffix so the
// operator knows the side effect landed before the bail.
func emitKillCASErr(emit func(sextantproto.RPCResponse), agentID uuid.UUID, err error, containerStopped bool) error {
	if errors.Is(err, errKillCASExhausted) {
		msg := fmt.Sprintf("agent %s definition changed during kill (concurrent restart/archive); re-issue kill if still appropriate", agentID)
		if containerStopped {
			msg = fmt.Sprintf("agent %s definition changed during kill (concurrent restart/archive); the container was stopped — re-issue kill against the new incarnation if appropriate", agentID)
		}
		return emitErr(emit, sextantproto.ErrCodeBadRequest, msg)
	}
	return emitErr(emit, sextantproto.ErrCodeInternal, err.Error())
}

// findLiveIncarnation walks the incarnations bucket and returns the
// (incarnation, key) pair whose AgentUUID matches and whose EndedAt is
// nil. Returns (nil, "", nil) if there isn't one — that's an expected
// state, not an error.
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

// rpc.Handler is the return type of NewKillAgent; keep this import
// alive so a future cap-checker change in rpc.Handler's signature
// trips a compile error here.
var _ = rpc.VerbKillAgent
