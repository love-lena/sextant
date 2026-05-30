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

// ArchiveDeps bundles the dependencies the archive handler needs. Under
// the declarative model archive_agent is a desired-state edit
// (spec.desired = archived); the reconciler tears the container down +
// reclaims the volume (sole actuator).
type ArchiveDeps struct {
	Definitions  AgentMutableKV
	Incarnations AgentMutableKV
	Containers   ContainerRunner
	Volumes      VolumeManager
	// Enqueue hints the reconciler to converge the archived intent now.
	Enqueue ReconcileEnqueuer
	Now     func() time.Time
}

// NewArchiveAgent returns a Handler for the archive_agent verb. Under
// the declarative model this writes spec.desired=archived and enqueues a
// reconcile — the reconciler stops + tears the container down and
// reclaims the per-agent volume. The name release is a property of the
// archived record (agentNameInUse skips archived defs).
//
//  1. Decode args; resolve the def.
//  2. Idempotent on already-archived intent.
//  3. CAS spec.desired = archived (retry on concurrent status writes).
//  4. Enqueue a reconcile; reply {ok: true}.
func NewArchiveAgent(deps ArchiveDeps) rpc.Handler {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		var args sextantproto.ArchiveAgentRequest
		if err := json.Unmarshal(req.Payload, &args); err != nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest,
				fmt.Sprintf("decode archive_agent payload: %v", err))
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
		var def sextantproto.AgentDefinition
		if err := json.Unmarshal(defEntry.Value(), &def); err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("decode definition: %v", err))
		}

		// 2. Idempotent on already-archived intent.
		if def.Spec.Desired == sextantproto.DesiredArchived {
			return emitOK(emit, sextantproto.ArchiveAgentResponse{OK: true})
		}

		// 3. CAS spec.desired = archived.
		if err := setDesired(ctx, deps.Definitions, args.AgentID, defEntry, sextantproto.DesiredArchived, deps.Now); err != nil {
			if errors.Is(err, errDesiredCASExhausted) {
				return emitErr(emit, sextantproto.ErrCodeBadRequest,
					fmt.Sprintf("agent %s definition changed during archive (concurrent restart/stop); re-issue archive if still appropriate", args.AgentID))
			}
			return emitErr(emit, sextantproto.ErrCodeInternal, err.Error())
		}
		if deps.Enqueue != nil {
			deps.Enqueue.Enqueue(args.AgentID)
		}
		return emitOK(emit, sextantproto.ArchiveAgentResponse{OK: true})
	}
}

// Compile-time sanity check that the verb constant exists.
var _ = rpc.VerbArchiveAgent
