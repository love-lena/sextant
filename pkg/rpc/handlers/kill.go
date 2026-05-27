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

		// 2. Load definition.
		defEntry, err := deps.Definitions.Get(ctx, args.AgentID.String())
		if err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				return emitErr(emit, sextantproto.ErrCodeAgentNotFound,
					fmt.Sprintf("no agent with uuid %s", args.AgentID))
			}
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("load definition: %v", err))
		}
		var def sextantproto.AgentDefinition
		if err := json.Unmarshal(defEntry.Value(), &def); err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("decode definition: %v", err))
		}
		// initialDefRevision pins the revision we just read so the
		// final def writes (both the no-live-incarnation fast path and
		// the post-stop path) can refuse to clobber a concurrent
		// restart_agent / archive_agent commit. Without this guard,
		// kill would stop the (stale) old incarnation, then plain-Put
		// lifecycle=defined back, overwriting the new incarnation a
		// concurrent restart_agent just committed and leaving its
		// container running orphaned. Codex 7th-round adversarial
		// review pinned that as a real interleaving.
		initialDefRevision := defEntry.Revision()

		// 3. Find live incarnation.
		inc, incKey, err := findLiveIncarnation(ctx, deps.Incarnations, args.AgentID)
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("find live incarnation: %v", err))
		}
		if inc == nil {
			// No live incarnation — fall through to lifecycle flip so a
			// caller can use kill_agent to put a stuck agent back to
			// defined without errors. CAS the write so we don't
			// overwrite a concurrent restart_agent that's about to
			// land a fresh incarnation.
			def.Lifecycle = sextantproto.LifecycleDefined
			def.Version++
			def.UpdatedAt = sextantproto.AtTimestamp(deps.Now().UTC())
			raw, mErr := json.Marshal(def)
			if mErr != nil {
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("marshal definition: %v", mErr))
			}
			if _, err := deps.Definitions.Update(ctx, args.AgentID.String(), raw, initialDefRevision); err != nil {
				if errors.Is(err, jetstream.ErrKeyExists) {
					return emitErr(emit, sextantproto.ErrCodeBadRequest,
						fmt.Sprintf("agent %s definition changed during kill (concurrent restart/archive); re-issue kill if still appropriate", args.AgentID))
				}
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("update definition: %v", err))
			}
			return emitOK(emit, sextantproto.KillAgentResponse{OK: true})
		}

		// 4. Stop the container.
		if inc.ContainerID != "" {
			if err := deps.Containers.Stop(ctx, inc.ContainerID, grace); err != nil {
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("stop container %s: %v", inc.ContainerID, err))
			}
		}

		// 5. Update the incarnation.
		now := deps.Now().UTC()
		ended := sextantproto.AtTimestamp(now)
		inc.State = sextantproto.IncarnationExited
		inc.EndedAt = &ended
		if err := putJSON(ctx, deps.Incarnations, incKey, *inc); err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("update incarnation: %v", err))
		}

		// 6. Flip the definition back to defined via CAS. If a
		// concurrent restart_agent has committed a new incarnation
		// between step 2 and here, the revision moved and we bail
		// rather than clobbering its work. The operator sees a clear
		// error and can re-issue kill against the new incarnation.
		def.Lifecycle = sextantproto.LifecycleDefined
		def.Version++
		def.UpdatedAt = sextantproto.AtTimestamp(now)
		raw, mErr := json.Marshal(def)
		if mErr != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("marshal definition: %v", mErr))
		}
		if _, err := deps.Definitions.Update(ctx, args.AgentID.String(), raw, initialDefRevision); err != nil {
			if errors.Is(err, jetstream.ErrKeyExists) {
				return emitErr(emit, sextantproto.ErrCodeBadRequest,
					fmt.Sprintf("agent %s definition changed during kill (concurrent restart/archive); the container was stopped — re-issue kill against the new incarnation if appropriate", args.AgentID))
			}
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("flip lifecycle to defined: %v", err))
		}
		return emitOK(emit, sextantproto.KillAgentResponse{OK: true})
	}
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
