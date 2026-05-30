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

// RestartDeps bundles the deps NewRestartAgent needs. Under the
// declarative model restart_agent is a re-actuation nonce bump (k8s
// `rollout restart`-style, RFC §5 lead) — it edits spec.reactuation_nonce
// and enqueues a reconcile; the reconciler builds the fresh incarnation
// (sole actuator). It no longer stops/starts containers itself.
//
// The runtime-bearing fields are retained so the daemon can build one
// dep bag; the handler reads only the KV + enqueue surfaces.
type RestartDeps struct {
	Definitions  AgentMutableKV
	Incarnations AgentMutableKV
	Containers   ContainerRunner
	// Enqueue hints the reconciler to actuate the fresh incarnation now.
	Enqueue ReconcileEnqueuer
	Now     func() time.Time
}

// restartCASRetries caps how many times the nonce bump retries on a CAS
// conflict before surfacing BAD_REQUEST.
const restartCASRetries = 3

// NewRestartAgent returns a Handler for the restart_agent verb. Under
// the declarative model restart has no natural desired-state, so it
// becomes a re-actuation nonce on the record (RFC §5 lead): bump
// spec.reactuation_nonce; the reconciler re-actuates a fresh incarnation
// when status.observed_nonce < spec.reactuation_nonce. Bumping the nonce
// is the LOCKED restart semantics.
//
// A restart also re-asserts desired=run: an operator who restarts a
// paused/lost agent means "make it run again." PreserveSession is
// recorded on the spec so the actuator resumes the SDK session.
//
//  1. Decode args; resolve the def.
//  2. CAS bump spec.reactuation_nonce (+ desired=run, + session-resume
//     intent), retrying on concurrent status writes.
//  3. Enqueue a reconcile; reply {ok: true}.
func NewRestartAgent(deps RestartDeps) rpc.Handler {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		var args sextantproto.RestartAgentRequest
		if err := json.Unmarshal(req.Payload, &args); err != nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest,
				fmt.Sprintf("decode restart_agent payload: %v", err))
		}
		if args.AgentID == uuid.Nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest, "agent_id is required")
		}

		entry, err := deps.Definitions.Get(ctx, args.AgentID.String())
		if err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				return emitErr(emit, sextantproto.ErrCodeAgentNotFound,
					fmt.Sprintf("no agent with uuid %s", args.AgentID))
			}
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("load definition: %v", err))
		}

		now := deps.Now
		var lastErr error
		for attempt := 0; attempt < restartCASRetries; attempt++ {
			if attempt > 0 {
				fresh, gerr := deps.Definitions.Get(ctx, args.AgentID.String())
				if gerr != nil {
					return emitErr(emit, sextantproto.ErrCodeInternal,
						fmt.Sprintf("re-read definition: %v", gerr))
				}
				entry = fresh
			}
			var def sextantproto.AgentDefinition
			if err := json.Unmarshal(entry.Value(), &def); err != nil {
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("decode definition: %v", err))
			}
			if def.Spec.Desired == sextantproto.DesiredArchived {
				return emitErr(emit, sextantproto.ErrCodeBadRequest,
					fmt.Sprintf("agent %s is archived; spawn a new agent instead", args.AgentID))
			}
			// The LOCKED restart semantics: bump the re-actuation nonce.
			def.Spec.ReactuationNonce++
			// A restart re-asserts run intent (restart a paused/lost agent
			// means "make it run").
			def.Spec.Desired = sextantproto.DesiredRun
			// Record the session-resume intent so the actuator resumes.
			if args.PreserveSession && def.Spec.Runtime.SessionID != nil {
				// SessionID already on the spec; the actuator resumes it via
				// the reactuation. Nothing extra to stamp — PreserveSession's
				// effect is "keep the recorded session," which is the default
				// once the nonce drives a fresh incarnation with the existing
				// def.Spec.Runtime.SessionID present.
				_ = args.PreserveSession
			} else if !args.PreserveSession {
				// Fresh session: clear the recorded session id so the
				// reactuation starts clean (the sidecar's first turn writes
				// a new one back).
				def.Spec.Runtime.SessionID = nil
			}
			def.Version++
			def.UpdatedAt = sextantproto.AtTimestamp(now().UTC())
			raw, mErr := json.Marshal(def)
			if mErr != nil {
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("marshal definition: %v", mErr))
			}
			_, lastErr = deps.Definitions.Update(ctx, args.AgentID.String(), raw, entry.Revision())
			if lastErr == nil {
				if deps.Enqueue != nil {
					deps.Enqueue.Enqueue(args.AgentID)
				}
				return emitOK(emit, sextantproto.RestartAgentResponse{AgentID: args.AgentID, OK: true})
			}
			if !errors.Is(lastErr, jetstream.ErrKeyExists) {
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("bump reactuation nonce: %v", lastErr))
			}
			// CAS conflict — loop and re-read.
		}
		return emitErr(emit, sextantproto.ErrCodeBadRequest,
			fmt.Sprintf("agent %s definition changed during restart (concurrent stop/archive); re-issue restart if still appropriate", args.AgentID))
	}
}
