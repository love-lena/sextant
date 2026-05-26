package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/authjwt"
	"github.com/love-lena/sextant/pkg/containermgr"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/templates"
)

// RestartDeps bundles the deps NewRestartAgent needs. It is a strict
// superset of KillDeps (we have to stop the live incarnation) plus a
// subset of SpawnDeps (we have to start a new one). Re-using SpawnDeps
// directly would force callers to populate fields irrelevant to
// restart; the narrower bundle keeps the wiring honest.
type RestartDeps struct {
	Definitions   AgentMutableKV
	Incarnations  AgentMutableKV
	Containers    ContainerRunner
	// Volumes lets restart re-attach (and, on first spawn after a
	// claude_seed change, populate) the per-agent claude_seed volume.
	// May be nil in tests that don't exercise the seed flow.
	Volumes      VolumeManager
	// Templates is required when seed mode is "copy-on-spawn" so the
	// restart path can re-resolve claude_seed / claude_seed_mode and
	// re-attach the named volume. May be nil; restart will then fall
	// back to the legacy "no seed mount" behavior, which is fine for
	// agents that don't use claude_seed.
	Templates     templates.KV
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
// When args.PreserveSession is true and the definition has a
// previously-recorded SDK session id, the new container inherits
// SEXTANT_SESSION_ID so the sidecar's first turn resumes the prior
// Claude conversation rather than starting fresh. See
// [[bug-restart-preserve-session-noop]].
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

		// Env is assembled by the same buildContainerEnv helper the spawn
		// path uses so the two can't drift on the well-known SEXTANT_*
		// keys. The pre-helper restart path silently dropped
		// ANTHROPIC_API_KEY, SEXTANT_MODEL, SEXTANT_PERMISSION_MODE, and
		// SEXTANT_SESSION_ID — see [[bug-restart-no-api-key-forwarding]]
		// and [[bug-restart-preserve-session-noop]].
		model := def.Runtime.Model
		if strings.TrimSpace(model) == "" {
			model = DefaultModel
		}
		// Only forward the session id when the operator asked us to
		// preserve it; otherwise the restart starts a fresh SDK session
		// and the next sidecar turn writes a new session id back into
		// def.Runtime.SessionID via CAS.
		var sessionID string
		if args.PreserveSession && def.Runtime.SessionID != nil {
			sessionID = *def.Runtime.SessionID
		}
		envVars := buildContainerEnv(containerEnvInput{
			AgentUUID:      def.UUID,
			AgentName:      def.Name,
			IncarnationID:  newIncID,
			HostID:         deps.HostID,
			NATSURL:        deps.NATSURL,
			NATSUser:       deps.NATSUser,
			NATSPassword:   deps.NATSPassword,
			JWT:            jwt,
			MCPURL:         deps.MCPURL,
			Model:          model,
			PermissionMode: permissionCeilingToSDKMode(def.Runtime.PermissionCeil),
			APIKey:         hostAPIKey(),
			SessionID:      sessionID,
			InitialPrompt:  def.Runtime.InitialPrompt,
			EnvOverlay:     def.Sandbox.Env,
		})
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
		mounts := []containermgr.MountSpec{{HostPath: workspace, ContainerPath: WorkspaceMountPath}}
		// Re-apply the claude_seed mount on restart. For copy-on-spawn
		// mode this re-attaches the per-agent named volume — populated
		// on the first spawn, idempotent here — so the SDK's session
		// journal under /home/agent/.claude/projects survives the
		// restart. Without this, --preserve-session would silently fail
		// because the new container can't read the journal the SDK
		// recorded in the previous incarnation's volume. See
		// plans/issues/bug-claude-seed-readonly-breaks-session-persistence.md.
		if deps.Templates != nil && def.Template != "" {
			tpl, err := templates.LoadFromKV(ctx, deps.Templates, def.Template)
			if err == nil && tpl.ClaudeSeed != "" {
				seedPath, expErr := templates.ExpandClaudeSeed(tpl.ClaudeSeed)
				if expErr == nil {
					seedMount, _, sErr := buildClaudeSeedMount(ctx, SpawnDeps{Volumes: deps.Volumes}, tpl.ResolveClaudeSeedMode(), seedPath, def.UUID, def.Sandbox.Image)
					if sErr == nil {
						mounts = append(mounts, seedMount)
					}
				}
			}
		}
		spec := containermgr.ContainerSpec{
			Name:       containerName(def.Name, newIncID),
			Image:      def.Sandbox.Image,
			Cmd:        []string{"/opt/sextant/sidecar/entrypoint.sh"},
			Env:        envVars,
			Mounts:     mounts,
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
			//nolint:contextcheck // rollback intentionally uses a fresh ctx — the request ctx may already be canceled
			rollbackBackgroundStop(deps.Containers, container.ID)
			// Step 2 already marked the old incarnation as exited;
			// step 4 succeeded but its container has now been stopped
			// by the rollback above. There are zero live incarnations
			// behind this AgentDefinition, so the lifecycle must flip
			// back to defined — otherwise list_agents lies ("running"
			// with no container) and the operator's recovery path is
			// unclear. Use the same shape step-4's rollback uses so
			// both partial-failure points converge on the same state.
			def.Lifecycle = sextantproto.LifecycleDefined
			def.Version++
			def.UpdatedAt = sextantproto.AtTimestamp(deps.Now().UTC())
			_ = putJSON(ctx, deps.Definitions, def.UUID.String(), def)
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("persist new incarnation: %v", err))
		}

		// 6. Bump the definition version. SEXTANT_SESSION_ID was
		// forwarded above when args.PreserveSession is true; the
		// sidecar's session-id capture path persists any new session id
		// it observes back onto def.Runtime.SessionID via CAS.
		def.Lifecycle = sextantproto.LifecycleRunning
		def.Version++
		def.UpdatedAt = sextantproto.AtTimestamp(deps.Now().UTC())
		if err := putJSON(ctx, deps.Definitions, def.UUID.String(), def); err != nil {
			//nolint:contextcheck // rollback intentionally uses a fresh ctx — see comment above
			rollbackBackgroundStopAndDelete(deps.Containers, deps.Incarnations, container.ID, newIncID.String())
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("flip lifecycle to running: %v", err))
		}

		return emitOK(emit, sextantproto.RestartAgentResponse{AgentID: def.UUID, OK: true})
	}
}

// rollbackBackgroundStop force-stops a container on a fresh background
// ctx. The request ctx may already be canceled by the time rollback
// runs, so we detach. Best-effort: errors are swallowed.
func rollbackBackgroundStop(c ContainerRunner, id string) {
	rbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = c.Stop(rbCtx, id, 5*time.Second)
}

// rollbackBackgroundStopAndDelete is rollbackBackgroundStop + delete
// the supplied incarnation KV key. Same fresh-ctx semantics.
func rollbackBackgroundStopAndDelete(c ContainerRunner, incs AgentMutableKV, id, incKey string) {
	rbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = c.Stop(rbCtx, id, 5*time.Second)
	_ = incs.Delete(rbCtx, incKey)
}
