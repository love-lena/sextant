package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant-initial/pkg/authjwt"
	"github.com/love-lena/sextant-initial/pkg/containermgr"
	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// RestartDeps bundles the deps NewRestartAgent needs. It is a strict
// superset of KillDeps (we have to stop the live incarnation) plus a
// subset of SpawnDeps (we have to start a new one). Re-using SpawnDeps
// directly would force callers to populate fields irrelevant to
// restart; the narrower bundle keeps the wiring honest.
type RestartDeps struct {
	Definitions   AgentMutableKV
	Incarnations  AgentMutableKV
	Containers    interface {
		ContainerRunner
		ContainerExecRunner
	}
	CA            *authjwt.CA
	WorkspaceRoot string
	HostID        string
	NATSURL       string
	NATSUser      string
	NATSPassword  string
	MCPURL        string
	Issuer        string
	TestRunLabel  string
	Now           func() time.Time
}

// NewRestartAgent returns a Handler for the restart_agent verb. Flow:
//
//  1. Decode args; resolve the AgentDefinition.
//  2. Stop the live incarnation (if any) — reuses findLiveIncarnation
//     so the kill semantics line up with kill_agent.
//  3. Mark the old incarnation as exited.
//  4. Re-issue a fresh JWT + start a new container with the same
//     definition's image/env/mounts.
//  5. Persist the new incarnation; bump the definition's version and
//     flip lifecycle back to running.
//  6. Reply with the agent UUID + ok=true.
//
// PreserveSession is recorded but has no effect today — M12 ships no
// session-continuity machinery (no driver loop). The flag is accepted
// for forward-compat so a future restart-with-session handler doesn't
// have to break the wire shape.
//
// Rollback: if step 4 fails after step 2 succeeded, the agent is left
// in lifecycle=defined with no live container. That's the same state
// a kill_agent on a healthy agent would produce, so the caller can
// recover by re-spawning. The alternative — try to re-create the old
// incarnation — opens a much bigger rollback surface and isn't worth
// the M12 complexity.
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

		// 1. Resolve the definition.
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

		// 2 + 3. Stop the live incarnation. Best-effort — if it's
		// already gone we still want to spawn a fresh one.
		now := deps.Now().UTC()
		oldInc, oldKey, err := findLiveIncarnation(ctx, deps.Incarnations, args.AgentID)
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("find live incarnation: %v", err))
		}
		if oldInc != nil {
			if oldInc.ContainerID != "" {
				if err := deps.Containers.Stop(ctx, oldInc.ContainerID, defaultGraceSeconds*time.Second); err != nil {
					return emitErr(emit, sextantproto.ErrCodeInternal,
						fmt.Sprintf("stop old container %s: %v", oldInc.ContainerID, err))
				}
			}
			ended := sextantproto.AtTimestamp(now)
			oldInc.State = sextantproto.IncarnationExited
			oldInc.EndedAt = &ended
			if err := putJSON(ctx, deps.Incarnations, oldKey, *oldInc); err != nil {
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("mark old incarnation exited: %v", err))
			}
		}

		// 4. Start a new container. We reuse the image / env / mounts
		// already baked into the AgentDefinition. The workspace dir is
		// the same per-UUID dir spawn created; we reuse it so an
		// in-progress edit survives the restart.
		newIncID := uuid.New()
		jwt, err := deps.CA.Issue(authjwt.Claims{
			AgentUUID:     def.UUID,
			IncarnationID: newIncID,
			Capabilities:  append([]string(nil), def.Tools...),
			IssuedAt:      now,
			ExpiresAt:     now.Add(SpawnJWTLifetime),
			Issuer:        deps.Issuer,
		})
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("issue jwt: %v", err))
		}
		workspace, err := ensureWorkspaceDir(deps.WorkspaceRoot, def.UUID.String())
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("ensure workspace: %v", err))
		}

		envVars := map[string]string{
			"SEXTANT_AGENT_UUID":     def.UUID.String(),
			"SEXTANT_AGENT_NAME":     def.Name,
			"SEXTANT_INCARNATION_ID": newIncID.String(),
			"SEXTANT_HOST_ID":        deps.HostID,
			"SEXTANT_NATS_URL":       deps.NATSURL,
			"SEXTANT_NATS_USER":      deps.NATSUser,
			"SEXTANT_NATS_PASSWORD":  deps.NATSPassword,
			"SEXTANT_JWT":            jwt,
			"SEXTANT_MCP_URL":        deps.MCPURL,
		}
		for k, v := range def.Sandbox.Env {
			envVars[k] = v
		}
		labels := map[string]string{
			LabelAgentUUID:     def.UUID.String(),
			LabelAgentName:     def.Name,
			LabelHostID:        deps.HostID,
			LabelIncarnationID: newIncID.String(),
			LabelTemplate:      def.Template,
		}
		if deps.TestRunLabel != "" {
			labels[LabelTestRun] = deps.TestRunLabel
		}
		spec := containermgr.ContainerSpec{
			Name:       containerName(def.Name, newIncID),
			Image:      def.Sandbox.Image,
			Cmd:        []string{"/opt/sextant/sidecar/entrypoint.sh"},
			Env:        envVars,
			Mounts:     []containermgr.MountSpec{{HostPath: workspace, ContainerPath: WorkspaceMountPath}},
			Labels:     labels,
			AutoRemove: true,
		}
		container, err := deps.Containers.Run(ctx, spec)
		if err != nil {
			// Restart is "best-effort across a failure point": leave the
			// agent in lifecycle=defined so kill_agent + spawn_agent can
			// recover. Flip the lifecycle here so the operator sees a
			// stable state.
			def.Lifecycle = sextantproto.LifecycleDefined
			def.Version++
			def.UpdatedAt = sextantproto.AtTimestamp(deps.Now().UTC())
			_ = putJSON(ctx, deps.Definitions, def.UUID.String(), def)
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("start new container: %v", err))
		}

		// 5. Persist the new incarnation.
		newInc := sextantproto.AgentIncarnation{
			IncarnationID: newIncID,
			AgentUUID:     def.UUID,
			ContainerID:   container.ID,
			StartedAt:     sextantproto.AtTimestamp(now),
			HostID:        deps.HostID,
			State:         sextantproto.IncarnationStarting,
		}
		if err := putJSON(ctx, deps.Incarnations, newIncID.String(), newInc); err != nil {
			// Container is up but we can't record it — stop it so we
			// don't leak.
			rbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = deps.Containers.Stop(rbCtx, container.ID, 5*time.Second)
			cancel()
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("persist new incarnation: %v", err))
		}

		// 6. Bump the definition version. preserve_session is reserved
		// for future use; we record the request flag in details for
		// audit but otherwise the spec says no behavior change today.
		def.Lifecycle = sextantproto.LifecycleRunning
		def.Version++
		def.UpdatedAt = sextantproto.AtTimestamp(deps.Now().UTC())
		if err := putJSON(ctx, deps.Definitions, def.UUID.String(), def); err != nil {
			// Definition KV write failed — try to roll back the new
			// incarnation/container so we don't leave a running
			// container with no "running" definition.
			rbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = deps.Containers.Stop(rbCtx, container.ID, 5*time.Second)
			_ = deps.Incarnations.Delete(rbCtx, newIncID.String())
			cancel()
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("flip lifecycle to running: %v", err))
		}

		// PreserveSession is accepted but ignored. Log to stderr so
		// the operator notices the no-op semantics rather than wondering
		// why session state vanished.
		if args.PreserveSession {
			fmt.Fprintf(os.Stderr, "restart_agent: preserve_session=true requested but ignored (no driver loop in phase 1)\n")
		}
		return emitOK(emit, sextantproto.RestartAgentResponse{AgentID: def.UUID, OK: true})
	}
}
