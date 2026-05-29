package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/authjwt"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/templates"
	"github.com/love-lena/sextant/pkg/worktree"
)

// RestartDeps bundles the deps NewRestartAgent needs. It is a strict
// superset of KillDeps (we have to stop the live incarnation) plus a
// subset of SpawnDeps (we have to start a new one). Re-using SpawnDeps
// directly would force callers to populate fields irrelevant to
// restart; the narrower bundle keeps the wiring honest.
type RestartDeps struct {
	Definitions  AgentMutableKV
	Incarnations AgentMutableKV
	Containers   ContainerRunner
	// Volumes lets restart re-attach (and, on first spawn after a
	// claude_seed change, populate) the per-agent claude_seed volume.
	// May be nil in tests that don't exercise the seed flow.
	Volumes VolumeManager
	// Templates is required when seed mode is "copy-on-spawn" so the
	// restart path can re-resolve claude_seed / claude_seed_mode and
	// re-attach the named volume. May be nil; restart will then fall
	// back to the legacy "no seed mount" behavior, which is fine for
	// agents that don't use claude_seed.
	Templates templates.KV
	// AgentsDataRoot is the host dir under which the per-agent
	// claude-projects bind-mount lives (mirrors SpawnDeps.AgentsDataRoot).
	// Restart MUST re-apply this mount or the restarted incarnation's SDK
	// session journal lands inside the container instead of the
	// host-readable path `sextant agents context` reads. Empty disables
	// the mount (legacy daemons + unit tests).
	AgentsDataRoot string
	// Worktree, when non-nil, lets restart resolve the SAME worktree
	// spawn created (by the deterministic spawn-worktree name) and
	// re-mount it as /workspace — the lossless-projection requirement
	// (RFC §5.4). Without it, a worktree agent restarts into the M11
	// stop-gap dir and loses its git working tree. nil/no-worktree-class
	// agents fall back to the stop-gap dir, as before.
	Worktree WorktreeProvider
	// RepoRoot mirrors SpawnDeps.RepoRoot. When set and the restarted
	// agent runs in a worktree, restart re-applies the <RepoRoot>/.git
	// bind so the worktree's `.git` pointer resolves inside the
	// container — one of the three mounts spawn added but pre-C0 restart
	// silently dropped. Empty disables the git-dir mount.
	RepoRoot      string
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

// restartCASRetries caps how many times the final definition commit
// retries on a CAS conflict before rolling back the new incarnation.
// Each retry re-reads the def and re-evaluates the archived guard;
// 3 is generous for the realistic operator-actions-in-flight case
// (archive_agent / kill_agent / update_agent racing).
const restartCASRetries = 3

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
		// initialDefRevision pins the revision we read above. The CAS
		// loop at the end of this handler bails if the def's revision
		// moves before we commit — that catches the kill_agent /
		// archive_agent / update_agent race where the operator
		// intervenes mid-restart. The CAS itself would already reject
		// the stale write, but checking the revision explicitly lets
		// us rollback the new incarnation we just spawned instead of
		// blindly retrying against an external writer's state.
		initialDefRevision := defEntry.Revision()

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
		// Resolve the workspace as a pure projection of the definition.
		// A worktree-class agent (def.Sandbox.Mounts lists "worktree")
		// re-mounts the SAME worktree spawn created — resolved by the
		// deterministic spawn-worktree name, never re-Created — so the
		// git working tree survives the restart. Everything else falls
		// back to the per-UUID stop-gap dir spawn used. This is the
		// workspace half of the lossless-projection guarantee (RFC §5.4).
		workspace, usingWorktree, err := resolveRestartWorkspace(ctx, deps, def)
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("resolve workspace: %v", err))
		}

		// Per-agent gitconfig — one of the three mounts spawn added but
		// pre-C0 restart silently dropped. The file is per-UUID and
		// idempotent (identical content each incarnation), so we write
		// it unconditionally and don't register a rollback: a leftover
		// file is harmless and the next spawn/restart rewrites it.
		gitconfigPath, _, err := writeAgentGitConfig(deps.WorkspaceRoot, def.UUID, def.Name)
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("write gitconfig: %v", err))
		}

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

		specIn := agentContainerSpecInput{
			Def:               def,
			IncarnationID:     newIncID,
			JWT:               jwt,
			HostID:            deps.HostID,
			NATSURL:           deps.NATSURL,
			NATSUser:          deps.NATSUser,
			NATSPassword:      deps.NATSPassword,
			MCPURL:            deps.MCPURL,
			Model:             model,
			SessionID:         sessionID,
			APIKey:            hostAPIKey(),
			TestRunLabel:      deps.TestRunLabel,
			WorkspacePath:     workspace,
			GitConfigHostPath: gitconfigPath,
		}
		// git-dir bind for worktree agents — resolves the worktree's
		// `.git` pointer. Mirrors spawn exactly (RFC §5.4 + the
		// bug-worktree-gitdir-unreachable fix).
		if usingWorktree && deps.RepoRoot != "" {
			specIn.GitDirHostPath = filepath.Join(deps.RepoRoot, ".git")
		}
		// SSH read-only bind — template opt-in, read off the def's
		// cloned mount classes. Was dropped pre-C0.
		if mountClassListed(def.Sandbox.Mounts, templates.MountClassSSH) {
			home, err := os.UserHomeDir()
			if err != nil {
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("resolve home dir for ssh mount: %v", err))
			}
			specIn.SSHHostPath = filepath.Join(home, ".ssh")
		}
		// Re-attach the claude_seed mount. For copy-on-spawn mode this
		// re-attaches the per-agent named volume — populated on the first
		// spawn, idempotent here — so the SDK's session journal under
		// /home/agent/.claude/projects survives the restart. Without it,
		// --preserve-session would silently fail because the new
		// container can't read the journal the SDK recorded in the
		// previous incarnation's volume. See
		// plans/issues/bug-claude-seed-readonly-breaks-session-persistence.md.
		if deps.Templates != nil && def.Template != "" {
			tpl, err := templates.LoadFromKV(ctx, deps.Templates, def.Template)
			if err == nil && tpl.ClaudeSeed != "" {
				seedPath, expErr := templates.ExpandClaudeSeed(tpl.ClaudeSeed)
				if expErr == nil {
					seedMount, _, sErr := buildClaudeSeedMount(ctx, SpawnDeps{Volumes: deps.Volumes}, tpl.ResolveClaudeSeedMode(), seedPath, def.UUID, def.Sandbox.Image)
					if sErr == nil {
						specIn.ClaudeSeedMount = &seedMount
					}
				}
			}
		}
		// Re-apply the per-agent claude-projects bind-mount that
		// spawn_agent adds (overlays the seed volume at
		// /home/agent/.claude/projects). Without it, the restarted
		// incarnation's SDK session journal writes inside the container
		// and `sextant agents context` can never find it — the
		// dir-absent bug. The dir is idempotent and must persist across
		// restarts, so we ignore the rollback cleanup here.
		// See plans/issues/feat-agents-context-view.md.
		if deps.AgentsDataRoot != "" {
			if projectsHost, _, err := ensureAgentProjectsDir(deps.AgentsDataRoot, def.UUID); err == nil {
				specIn.ClaudeProjectsHostPath = projectsHost
			}
		}

		spec := buildAgentContainerSpec(specIn)
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

		// 6. Commit the new lifecycle state with a CAS write. Between
		// this handler's initial Get and the final write,
		// archive_agent / kill_agent / update_agent might have
		// changed the record. The Codex adversarial-review pinned the
		// archive case as critical: without CAS the restart would
		// overwrite an operator's archive with `running`, undoing the
		// name release and accepting prompts into a dead inbox.
		//
		// Loop: re-read the def, check guards (archived ⇒ rollback +
		// abort), apply our lifecycle/incarnation fields, attempt
		// Update with the revision we just read. On
		// jetstream.ErrKeyExists, another writer slipped in — re-read
		// + re-apply the guards. On retry exhaustion, rollback the
		// new incarnation we just spawned.
		//
		// CurrentIncarnationID is the authoritative anchor the
		// lifecycle watcher gates stale-envelope filtering on; setting
		// it here (before the new sidecar's `started` envelope reaches
		// the bus) closes the restart-handoff race a delayed `ended`
		// from the prior incarnation would otherwise win.
		var (
			finalDef sextantproto.AgentDefinition
			casErr   error
		)
		for attempt := 0; attempt < restartCASRetries; attempt++ {
			entry, err := deps.Definitions.Get(ctx, def.UUID.String())
			if err != nil {
				//nolint:contextcheck // rollback intentionally uses a fresh ctx — see comment above
				rollbackBackgroundStopAndDelete(deps.Containers, deps.Incarnations, container.ID, newIncID.String())
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("re-read definition before commit: %v", err))
			}
			if entry.Revision() != initialDefRevision {
				// An external writer modified the def between our
				// initial Get and now — kill_agent / archive_agent /
				// update_agent racing the restart. Bail with rollback
				// rather than blindly overwriting their work.
				// Distinguishing benign edits (e.g. Description) from
				// terminal transitions (archive / kill) at this layer
				// would require per-field intent; the conservative
				// choice is to fail-closed and ask the operator to
				// re-issue restart. See the Codex 6th-round review.
				var raced sextantproto.AgentDefinition
				_ = json.Unmarshal(entry.Value(), &raced)
				//nolint:contextcheck // rollback intentionally uses a fresh ctx — see comment above
				rollbackBackgroundStopAndDelete(deps.Containers, deps.Incarnations, container.ID, newIncID.String())
				return emitErr(emit, sextantproto.ErrCodeBadRequest,
					fmt.Sprintf("agent %s definition changed during restart (lifecycle=%s); rolled back new incarnation — re-issue restart if still appropriate", def.UUID, raced.Lifecycle))
			}
			var fresh sextantproto.AgentDefinition
			if err := json.Unmarshal(entry.Value(), &fresh); err != nil {
				//nolint:contextcheck // rollback intentionally uses a fresh ctx — see comment above
				rollbackBackgroundStopAndDelete(deps.Containers, deps.Incarnations, container.ID, newIncID.String())
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("decode definition before commit: %v", err))
			}
			if fresh.Lifecycle == sextantproto.LifecycleArchived {
				// Defensive: revision check above should already catch
				// this, but keep the explicit check in case a future
				// path resets the revision.
				//nolint:contextcheck // rollback intentionally uses a fresh ctx — see comment above
				rollbackBackgroundStopAndDelete(deps.Containers, deps.Incarnations, container.ID, newIncID.String())
				return emitErr(emit, sextantproto.ErrCodeBadRequest,
					fmt.Sprintf("agent %s was archived during restart; rolled back new incarnation", def.UUID))
			}
			// Carry forward fields the freshly-read def may have that
			// our in-memory snapshot doesn't (Description / EscalateTo
			// edits from in-flight update_agent calls). Lifecycle +
			// CurrentIncarnationID + Version + UpdatedAt are this
			// handler's to own.
			fresh.Lifecycle = sextantproto.LifecycleRunning
			fresh.CurrentIncarnationID = newIncID
			fresh.Version++
			fresh.UpdatedAt = sextantproto.AtTimestamp(deps.Now().UTC())
			raw, mErr := json.Marshal(fresh)
			if mErr != nil {
				//nolint:contextcheck // rollback intentionally uses a fresh ctx — see comment above
				rollbackBackgroundStopAndDelete(deps.Containers, deps.Incarnations, container.ID, newIncID.String())
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("marshal definition: %v", mErr))
			}
			_, casErr = deps.Definitions.Update(ctx, fresh.UUID.String(), raw, entry.Revision())
			if casErr == nil {
				finalDef = fresh
				break
			}
			if !errors.Is(casErr, jetstream.ErrKeyExists) {
				//nolint:contextcheck // rollback intentionally uses a fresh ctx — see comment above
				rollbackBackgroundStopAndDelete(deps.Containers, deps.Incarnations, container.ID, newIncID.String())
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("flip lifecycle to running: %v", casErr))
			}
			// CAS conflict — loop and re-read.
		}
		if casErr != nil {
			//nolint:contextcheck // rollback intentionally uses a fresh ctx — see comment above
			rollbackBackgroundStopAndDelete(deps.Containers, deps.Incarnations, container.ID, newIncID.String())
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("flip lifecycle to running: gave up after %d CAS conflicts", restartCASRetries))
		}

		return emitOK(emit, sextantproto.RestartAgentResponse{AgentID: finalDef.UUID, OK: true})
	}
}

// resolveRestartWorkspace returns the /workspace host path for the
// restarted incarnation and whether it is a worktree.
//
// Worktree-class agents (def.Sandbox.Mounts lists "worktree") with a
// worktree provider wired re-mount the SAME worktree spawn created,
// resolved by the deterministic spawn-worktree name. We Resolve rather
// than Create: the worktree already exists from spawn (kill doesn't
// destroy it; archive does), and Create rejects an existing name. If
// the worktree can't be resolved (provider absent, name pruned, or the
// agent never used one) we fall back to the per-UUID stop-gap dir spawn
// uses for non-worktree agents — the same fallback materializeWorkspace
// applies on spawn when the worktree surface is disabled.
func resolveRestartWorkspace(ctx context.Context, deps RestartDeps, def sextantproto.AgentDefinition) (string, bool, error) {
	if deps.Worktree != nil && mountClassListed(def.Sandbox.Mounts, templates.MountClassWorktree) {
		name := worktree.SpawnWorktreeName(def.Template, def.UUID)
		path, ok, err := deps.Worktree.Resolve(ctx, name)
		if err != nil {
			return "", false, fmt.Errorf("resolve worktree %s: %w", name, err)
		}
		if ok {
			return path, true, nil
		}
		// Worktree not found — fall through to the stop-gap dir. The
		// agent either never ran in a worktree (daemon had the surface
		// disabled at spawn) or it was pruned; either way the stop-gap
		// dir is the safe, spawn-equivalent fallback.
	}
	path, err := ensureWorkspaceDir(deps.WorkspaceRoot, def.UUID.String())
	if err != nil {
		return "", false, err
	}
	return path, false, nil
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
