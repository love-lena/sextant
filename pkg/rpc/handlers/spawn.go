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
	"github.com/love-lena/sextant/pkg/containermgr"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/templates"
	"github.com/love-lena/sextant/pkg/worktree"
)

// AgentIncarnationsBucket is the canonical KV bucket name for agent
// incarnation records. Mirrors pkg/natsboot/layout.go's row.
const AgentIncarnationsBucket = "agent_incarnations"

// DefaultModel is the Claude model identifier the spawn handler sets
// on every agent whose template doesn't declare one. Mirrors the
// default referenced in specs/architecture.md §11b and the sidecar's
// own fallback.
const DefaultModel = "claude-opus-4-7[1m]"

// SpawnJWTLifetime is the per-incarnation JWT lifetime applied by the
// M11 spawn handler. Spec calls for 24h. Bump alongside the
// specs/components/sextantd.md §"M11 spawn flow" doc.
const SpawnJWTLifetime = 24 * time.Hour

// LabelAgentUUID etc. are the container labels every spawn stamps. Tests
// rely on these stable strings for cleanup.
const (
	LabelAgentUUID      = "sextant.agent_uuid"
	LabelAgentName      = "sextant.agent_name"
	LabelHostID         = "sextant.host_id"
	LabelIncarnationID  = "sextant.incarnation_id"
	LabelTemplate       = "sextant.template"
	LabelTestRun        = "sextant.test_run"
	WorkspaceMountPath  = "/workspace"
	defaultGraceSeconds = 10
)

// AgentMutableKV is the read+write surface the spawn handler needs on
// the agent_definitions and agent_incarnations buckets. Narrowed so
// tests can pass a fake without bringing JetStream up.
type AgentMutableKV interface {
	AgentKV
	Put(ctx context.Context, key string, value []byte) (uint64, error)
	Delete(ctx context.Context, key string, opts ...jetstream.KVDeleteOpt) error
}

// HistoryWriter records definition mutations to ClickHouse so the
// audit trail survives a NATS data-dir wipe. Narrow surface so the
// spawn handler doesn't need the full driver.Conn.
type HistoryWriter interface {
	Exec(ctx context.Context, query string, args ...any) error
}

// SpawnDeps bundles the dependencies the spawn handler needs. The
// daemon wires real values; tests substitute fakes.
type SpawnDeps struct {
	Definitions   AgentMutableKV
	Incarnations  AgentMutableKV
	Templates     templates.KV
	Containers    ContainerRunner
	CA            *authjwt.CA
	History       HistoryWriter
	WorkspaceRoot string
	// Worktree, when non-nil, is used to materialize the /workspace
	// mount for templates whose `mounts` field includes "worktree".
	// When nil or when the template doesn't request a worktree, the
	// spawn handler falls back to the M11 stop-gap dir under
	// WorkspaceRoot. The spawn-time worktree is named per
	// specs/architecture.md §11 "Worktree naming" via
	// worktree.SpawnWorktreeName.
	Worktree WorktreeProvider
	// RepoRoot is the host path of the main repository (the same
	// value the worktree manager uses as its RepoRoot). When set and
	// the spawn materializes a worktree, the spawn handler bind-mounts
	// <RepoRoot>/.git into the container at the same path so the
	// worktree's `.git` pointer file resolves inside the container.
	// Empty disables the gitdir mount (fallback workspaces don't need
	// it, and tests that don't exercise the worktree path leave it
	// empty).
	RepoRoot string
	// Volumes, when non-nil, manages per-agent Docker named volumes
	// (currently only the claude_seed copy-on-spawn volume). When nil,
	// templates with claude_seed_mode = "copy-on-spawn" fall back to the
	// legacy readonly-bind behavior so the spawn handler still works in
	// pre-volume-aware test harnesses. Production wiring passes the
	// containermgr.Manager (which satisfies both ContainerRunner and
	// VolumeManager).
	Volumes      VolumeManager
	HostID       string
	NATSURL      string
	NATSUser     string
	NATSPassword string
	MCPURL       string
	Issuer       string
	// TestRunLabel, when non-empty, stamps sextant.test_run=<value> on
	// every spawned container. Used by tests to scope cleanup. Empty in
	// production.
	TestRunLabel string
	// Now is injected for deterministic timestamps in tests.
	Now func() time.Time
}

// WorktreeProvider is the narrow surface the spawn handler needs on
// pkg/worktree. Defined here (consumer-side) so the handlers package
// doesn't depend on the worktree package; the daemon adapts its
// *worktree.Manager into this interface.
type WorktreeProvider interface {
	Create(ctx context.Context, name, baseBranch string, owningAgent uuid.UUID) (sextantproto.WorktreeInfo, error)
	Destroy(ctx context.Context, name string, force bool) error
}

// ContainerRunner is the subset of containermgr.Manager the handlers
// call. Mirroring it as an interface keeps the dependency direction
// clean and lets tests substitute a no-op runner without depending on
// docker SDK availability.
type ContainerRunner interface {
	Run(ctx context.Context, spec containermgr.ContainerSpec) (*containermgr.Container, error)
	Stop(ctx context.Context, id string, grace time.Duration) error
}

// VolumeManager is the subset of containermgr.Manager the spawn/archive
// handlers use to manage per-agent named volumes (today: the
// claude_seed copy-on-spawn volume). Defined here so tests can
// substitute a fake without spinning a real docker daemon.
type VolumeManager interface {
	EnsureVolume(ctx context.Context, name string, labels map[string]string) (created bool, err error)
	PopulateVolumeFromHostDir(ctx context.Context, volumeName, hostSrc, image string, cmd []string) error
	RemoveVolume(ctx context.Context, name string, force bool) error
}

// NewSpawnAgent returns a Handler that implements `spawn_agent`.
// Flow per specs/components/sextantd.md §"M11 spawn flow":
//
//  1. Decode + validate args.
//  2. Reject duplicate names (unique among non-archived definitions).
//  3. Resolve the template from KV.
//  4. Build a fresh AgentDefinition; persist as agent_definitions/<uuid>.
//  5. Append the initial agent_definitions_history row.
//  6. Materialize the M11 stop-gap workspace dir.
//  7. Build the AgentIncarnation record (state=starting).
//  8. Issue the per-incarnation JWT.
//  9. Build the container spec; Run it.
//  10. Persist agent_incarnations/<incarnation_id> with container ID.
//  11. Flip the definition's lifecycle to "running" + bump version.
//  12. Reply with the new agent UUID.
//
// Errors at any step roll back what we already wrote so the daemon
// doesn't accumulate half-spawned ghosts.
func NewSpawnAgent(deps SpawnDeps) rpc.Handler {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		var args sextantproto.SpawnAgentRequest
		if err := json.Unmarshal(req.Payload, &args); err != nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest,
				fmt.Sprintf("decode spawn_agent payload: %v", err))
		}
		if strings.TrimSpace(args.Name) == "" {
			return emitErr(emit, sextantproto.ErrCodeBadRequest, "name is required")
		}
		if strings.TrimSpace(args.Template) == "" {
			return emitErr(emit, sextantproto.ErrCodeBadRequest, "template is required")
		}

		// 2. Reject duplicates.
		if dup, err := agentNameInUse(ctx, deps.Definitions, args.Name); err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("check name uniqueness: %v", err))
		} else if dup {
			return emitErr(emit, sextantproto.ErrCodeBadRequest,
				fmt.Sprintf("agent name %q is already in use", args.Name))
		}

		// 3. Resolve template.
		tpl, err := templates.LoadFromKV(ctx, deps.Templates, args.Template)
		if err != nil {
			if errors.Is(err, templates.ErrNotFound) {
				return emitErr(emit, sextantproto.ErrCodeBadRequest,
					fmt.Sprintf("template %q not found in KV (run `sextant init` to seed defaults)", args.Template))
			}
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("load template %q: %v", args.Template, err))
		}

		now := deps.Now().UTC()
		agentUUID := uuid.New()
		incID := uuid.New()
		hostPin := args.HostPin
		hostID := deps.HostID

		// 4. Build + persist the AgentDefinition.
		def := sextantproto.AgentDefinition{
			UUID:        agentUUID,
			Name:        args.Name,
			Type:        "assistant",
			Template:    tpl.Name,
			Description: tpl.Description,
			Runtime: sextantproto.RuntimeConfig{
				Model:          tpl.Model,
				PermissionCeil: tpl.PermissionCeiling,
				InitialPrompt:  tpl.InitialPrompt,
			},
			Sandbox: sextantproto.SandboxConfig{
				Image:  tpl.Image,
				Mounts: append([]string(nil), tpl.Mounts...),
				Env:    cloneStringMap(tpl.Env),
			},
			Tools:                append([]string(nil), tpl.Permissions...),
			Lifecycle:            sextantproto.LifecycleDefined,
			CurrentIncarnationID: incID,
			Version:              1,
			CreatedAt:            sextantproto.AtTimestamp(now),
			UpdatedAt:            sextantproto.AtTimestamp(now),
		}
		if hostPin != "" {
			pin := hostPin
			def.HostPin = &pin
		}
		// Rollback ledger: every step that produces a side-effect pushes
		// its cleanup closure here. On any error before `committed` is
		// flipped, the deferred rollback walks the ledger in LIFO order
		// and undoes every step — workspace dir, KV entries, container,
		// the lot. This replaces the per-step ad-hoc deletes that
		// previously leaked the workspace and (on lifecycle-flip
		// failure) left a running container with no `running`
		// definition.
		var (
			committed bool
			rollbacks []func()
		)
		pushRollback := func(fn func()) {
			rollbacks = append(rollbacks, fn)
		}
		defer func() {
			if committed {
				return
			}
			// LIFO so cleanup mirrors the order operations were applied.
			for i := len(rollbacks) - 1; i >= 0; i-- {
				rollbacks[i]()
			}
		}()

		if err := putJSON(ctx, deps.Definitions, agentUUID.String(), def); err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("persist agent definition: %v", err))
		}
		//nolint:contextcheck // rollback closure intentionally outlives the request ctx — see fresh-background-ctx comment in the closure
		pushRollback(func() {
			// Use a fresh background ctx with a short timeout — the
			// request ctx may already be canceled by the time we
			// rollback. Same pattern for every cleanup below.
			rbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = deps.Definitions.Delete(rbCtx, agentUUID.String())
		})

		// 5. Append the initial agent_definitions_history row. Best-
		// effort: a history-table failure shouldn't abort a spawn — it
		// becomes an alertable event the operator can backfill. The
		// history table is append-only so we don't try to roll it back.
		if deps.History != nil {
			if err := insertDefinitionHistory(ctx, deps.History, def, "spawn"); err != nil {
				// Don't fail the spawn — log via stderr.
				fmt.Fprintf(os.Stderr, "spawn_agent: history insert failed for %s: %v\n",
					agentUUID, err)
			}
		}

		// 6. Materialize workspace. When the template's `mounts`
		// includes "worktree" and the daemon has a worktree provider
		// wired, create a per-incarnation worktree and mount that.
		// Otherwise, fall back to the M11 stop-gap dir under
		// WorkspaceRoot.
		workspace, usingWorktree, workspaceCleanup, err := materializeWorkspace(ctx, deps, tpl, agentUUID)
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("create workspace: %v", err))
		}
		pushRollback(workspaceCleanup)

		// 6a. Per-spawn gitconfig file. Bind-mounted into the container
		// at /home/agent/.gitconfig so the agent has a usable git
		// identity for commits. See plans/issues/feat-container-git-
		// config.md.
		gitconfigPath, gitconfigCleanup, err := writeAgentGitConfig(deps.WorkspaceRoot, agentUUID, args.Name)
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("write gitconfig: %v", err))
		}
		pushRollback(gitconfigCleanup)

		// 7+8. Build incarnation record (state=starting) + issue JWT.
		inc := sextantproto.AgentIncarnation{
			IncarnationID: incID,
			AgentUUID:     agentUUID,
			StartedAt:     sextantproto.AtTimestamp(now),
			HostID:        hostID,
			State:         sextantproto.IncarnationStarting,
		}
		jwt, err := deps.CA.Issue(authjwt.Claims{
			AgentUUID:     agentUUID,
			IncarnationID: incID,
			Capabilities:  append([]string(nil), tpl.Permissions...),
			IssuedAt:      now,
			ExpiresAt:     now.Add(SpawnJWTLifetime),
			Issuer:        deps.Issuer,
		})
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("issue jwt: %v", err))
		}

		// 9. Container spec. Env is assembled by buildContainerEnv so the
		// spawn and restart paths can't drift on the well-known
		// SEXTANT_* keys; the body of that helper mirrors
		// specs/components/sidecar-image.md §"Env vars" exactly. See
		// pkg/rpc/handlers/container_env.go.
		model := tpl.Model
		if strings.TrimSpace(model) == "" {
			model = DefaultModel
		}
		// Resume an existing SDK session when the definition has one
		// recorded. The sidecar's first turn captures the SDK-issued
		// session_id and writes it back to the definition, so the next
		// spawn of the same agent picks up where we left off. Restart
		// only forwards the session id when --preserve-session is true,
		// but spawn always does (a fresh definition has SessionID=nil).
		var sessionID string
		if def.Runtime.SessionID != nil {
			sessionID = *def.Runtime.SessionID
		}
		envVars := buildContainerEnv(containerEnvInput{
			AgentUUID:      agentUUID,
			AgentName:      def.Name,
			IncarnationID:  incID,
			HostID:         hostID,
			NATSURL:        deps.NATSURL,
			NATSUser:       deps.NATSUser,
			NATSPassword:   deps.NATSPassword,
			JWT:            jwt,
			MCPURL:         deps.MCPURL,
			Model:          model,
			PermissionMode: permissionCeilingToSDKMode(tpl.PermissionCeiling),
			APIKey:         hostAPIKey(),
			SessionID:      sessionID,
			InitialPrompt:  tpl.InitialPrompt,
			EnvOverlay:     tpl.Env,
		})

		labels := map[string]string{
			LabelAgentUUID:     agentUUID.String(),
			LabelAgentName:     def.Name,
			LabelHostID:        hostID,
			LabelIncarnationID: incID.String(),
			LabelTemplate:      tpl.Name,
		}
		if deps.TestRunLabel != "" {
			labels[LabelTestRun] = deps.TestRunLabel
		}

		// AutoRemove=true so a crashing sidecar can't leave a stopped
		// container around. Stop() force-removes anyway as a safety net.
		//
		// Cmd points at the sidecar entrypoint script. The image's
		// default CMD is /bin/bash (so the M9 smoke test stays
		// interactive); spawning agents always overrides it to run the
		// long-lived sidecar runtime.
		mounts := []containermgr.MountSpec{
			{HostPath: workspace, ContainerPath: WorkspaceMountPath},
		}
		// When the workspace is a worktree, the worktree's `.git` is a
		// pointer file that names <RepoRoot>/.git/worktrees/<branch>
		// using the host's absolute path. We must expose that exact
		// path inside the container so git operations resolve. Mount
		// rw because `git commit` writes blobs into <RepoRoot>/.git/
		// objects (worktrees share the main repo's object store) and
		// refs into <RepoRoot>/.git/worktrees/<branch>/. See
		// plans/issues/bug-worktree-gitdir-unreachable-in-container.md.
		if usingWorktree && deps.RepoRoot != "" {
			gitdir := filepath.Join(deps.RepoRoot, ".git")
			mounts = append(mounts, containermgr.MountSpec{
				HostPath:      gitdir,
				ContainerPath: gitdir,
			})
		}
		mounts = append(mounts, containermgr.MountSpec{
			HostPath:      gitconfigPath,
			ContainerPath: "/home/agent/.gitconfig",
			ReadOnly:      true,
		})
		// Template-opt-in: bind-mount the host's ~/.ssh read-only at
		// /home/agent/.ssh so the agent can authenticate to GitHub for
		// `git push`. Only fires when the template lists "ssh" in
		// `mounts` — defaults stay airtight. Read-only so a misbehaving
		// agent can't rewrite the operator's keys. See
		// plans/issues/feat-container-ssh-passthrough.md and
		// specs/components/sidecar-image.md.
		if wantsSSHMount(tpl) {
			home, err := os.UserHomeDir()
			if err != nil {
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("resolve home dir for ssh mount: %v", err))
			}
			mounts = append(mounts, containermgr.MountSpec{
				HostPath:      filepath.Join(home, ".ssh"),
				ContainerPath: "/home/agent/.ssh",
				ReadOnly:      true,
			})
		}
		// Template-declared seed for /home/agent/.claude. Two modes:
		//
		//   - "copy-on-spawn" (default when claude_seed is set): create a
		//     per-agent Docker named volume, populate it from the host
		//     seed dir on first spawn (idempotent on subsequent spawns),
		//     and mount it rw. This lets the Claude Agent SDK write its
		//     session journal under /home/agent/.claude/projects/ — which
		//     a readonly bind blocks. See
		//     plans/issues/bug-claude-seed-readonly-breaks-session-persistence.md.
		//
		//   - "readonly-bind" (legacy opt-in): bind-mount the host dir
		//     read-only. Suitable for one-shot agents that don't need
		//     SDK state persistence; multi-turn conversation does not
		//     work in this mode.
		//
		// templates.Validate already confirmed the path exists and is a
		// directory; we re-expand here so a `~/`-prefixed value (resolved
		// against the daemon-process's UserHomeDir) reaches containermgr
		// as an absolute path.
		if tpl.ClaudeSeed != "" {
			seedPath, err := templates.ExpandClaudeSeed(tpl.ClaudeSeed)
			if err != nil {
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("expand claude_seed: %v", err))
			}
			mode := tpl.ResolveClaudeSeedMode()
			seedMount, seedCleanup, err := buildClaudeSeedMount(ctx, deps, mode, seedPath, agentUUID, tpl.Image)
			if err != nil {
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("claude_seed (mode %q): %v", mode, err))
			}
			mounts = append(mounts, seedMount)
			if seedCleanup != nil {
				pushRollback(seedCleanup)
			}
		}

		spec := containermgr.ContainerSpec{
			Name:       containerName(def.Name, incID),
			Image:      tpl.Image,
			Cmd:        []string{"/opt/sextant/sidecar/entrypoint.sh"},
			Env:        envVars,
			Mounts:     mounts,
			Labels:     labels,
			AutoRemove: true,
		}
		container, err := deps.Containers.Run(ctx, spec)
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("start container: %v", err))
		}
		//nolint:contextcheck // rollback closure intentionally outlives the request ctx
		pushRollback(func() {
			rbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = deps.Containers.Stop(rbCtx, container.ID, 5*time.Second)
		})
		inc.ContainerID = container.ID

		// 10. Persist the incarnation.
		if err := putJSON(ctx, deps.Incarnations, incID.String(), inc); err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("persist agent incarnation: %v", err))
		}
		//nolint:contextcheck // rollback closure intentionally outlives the request ctx
		pushRollback(func() {
			rbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = deps.Incarnations.Delete(rbCtx, incID.String())
		})

		// 11. Flip definition lifecycle to running + bump version. This
		// is the last side-effect before the success reply; a failure
		// here means the KV is unhealthy *and* we still have a live
		// container — so we must roll the whole spawn back rather than
		// leave an inconsistent "definition=defined, container running"
		// state in the bus.
		def.Lifecycle = sextantproto.LifecycleRunning
		def.Version = 2
		def.UpdatedAt = sextantproto.AtTimestamp(deps.Now().UTC())
		if err := putJSON(ctx, deps.Definitions, agentUUID.String(), def); err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("flip lifecycle to running: %v", err))
		}
		if deps.History != nil {
			_ = insertDefinitionHistory(ctx, deps.History, def, "running")
		}

		committed = true
		return emitOK(emit, sextantproto.SpawnAgentResponse{AgentID: agentUUID})
	}
}

// agentNameInUse returns true if there's an entry in the definitions
// bucket whose Name matches and whose Lifecycle is not "archived".
//
// O(N) scan over the bucket; M11 has very few entries so this is fine.
// A secondary name index would make sense at scale but is not the M11
// hot path.
func agentNameInUse(ctx context.Context, kv AgentMutableKV, name string) (bool, error) {
	lister, err := kv.ListKeys(ctx)
	if err != nil {
		if errors.Is(err, jetstream.ErrNoKeysFound) || errors.Is(err, jetstream.ErrKeyNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("list keys: %w", err)
	}
	defer func() { _ = lister.Stop() }()
	for key := range lister.Keys() {
		entry, err := kv.Get(ctx, key)
		if err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				continue
			}
			return false, fmt.Errorf("get %s: %w", key, err)
		}
		var def sextantproto.AgentDefinition
		if err := json.Unmarshal(entry.Value(), &def); err != nil {
			// Garbage in the bucket — don't crash spawn over it, but
			// log so the operator notices. Silently continuing means
			// a corrupt blob with the requested name would be
			// invisible: the duplicate-name check would pass, and the
			// spawn would succeed against a poisoned bucket.
			fmt.Fprintf(os.Stderr, "spawn_agent: agentNameInUse: decode %s: %v\n", key, err)
			continue
		}
		if def.Name == name && def.Lifecycle != sextantproto.LifecycleArchived {
			return true, nil
		}
	}
	return false, nil
}

func putJSON(ctx context.Context, kv AgentMutableKV, key string, val any) error {
	raw, err := json.Marshal(val)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if _, err := kv.Put(ctx, key, raw); err != nil {
		return fmt.Errorf("kv put %s: %w", key, err)
	}
	return nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ensureWorkspaceDir creates ~/.local/share/sextant/spawn-workspaces/<uuid>/
// if missing. M11 stop-gap — used as the fallback when a template
// doesn't request a worktree mount (or the daemon has no worktree
// provider).
func ensureWorkspaceDir(root, agentUUID string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("workspace root is empty")
	}
	path := filepath.Join(root, agentUUID)
	if err := os.MkdirAll(path, 0o750); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", path, err)
	}
	return path, nil
}

// materializeWorkspace decides whether to create a per-incarnation
// worktree or fall back to the M11 stop-gap dir, and returns the
// resolved on-host path + a "is a worktree" flag + a cleanup closure
// for the rollback ledger.
//
// Rules:
//
//   - Template lists "worktree" in mounts AND deps.Worktree non-nil →
//     create a worktree via worktree.Create; cleanup removes the
//     worktree. usingWorktree=true.
//   - Otherwise → ensureWorkspaceDir; cleanup os.RemoveAll's the dir.
//     usingWorktree=false.
//
// The fallback path covers two scenarios: M11-style templates that
// never declared the mount, and templates that do declare it but
// land on a daemon where worktree.repo_root is unset (M14
// transitional state).
//
//nolint:contextcheck // rollback closure intentionally uses background ctx so a canceled request still cleans up
func materializeWorkspace(ctx context.Context, deps SpawnDeps, tpl templates.Template, agentUUID uuid.UUID) (string, bool, func(), error) {
	if wantsWorktreeMount(tpl) && deps.Worktree != nil {
		name := worktree.SpawnWorktreeName(tpl.Name, agentUUID)
		info, err := deps.Worktree.Create(ctx, name, "main", agentUUID)
		if err != nil {
			return "", false, nil, fmt.Errorf("worktree.Create %s: %w", name, err)
		}
		cleanup := func() {
			rbCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = deps.Worktree.Destroy(rbCtx, info.Name, true)
		}
		return info.Path, true, cleanup, nil
	}
	path, err := ensureWorkspaceDir(deps.WorkspaceRoot, agentUUID.String())
	if err != nil {
		return "", false, nil, err
	}
	cleanup := func() {
		_ = os.RemoveAll(path)
	}
	return path, false, cleanup, nil
}

// writeAgentGitConfig stages a per-spawn gitconfig file under root and
// returns its path + a cleanup closure that removes the file. The body
// matches plans/issues/feat-container-git-config.md: name = "sextant
// <agent-name>", email = "<uuid>@sextant.local", init.defaultBranch =
// main. The file is intentionally mode 0o644 (and bind-mounted
// read-only into the container) so the agent can read it but not
// rewrite the identity from inside the sandbox.
func writeAgentGitConfig(root string, agentUUID uuid.UUID, agentName string) (string, func(), error) {
	if root == "" {
		return "", nil, fmt.Errorf("workspace root is empty")
	}
	path := filepath.Join(root, "gitconfig-"+agentUUID.String())
	body := fmt.Sprintf("[user]\n\tname = sextant %s\n\temail = %s@sextant.local\n[init]\n\tdefaultBranch = main\n",
		agentName, agentUUID)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil { //nolint:gosec // bind-mounted ro into container; 0o644 lets the in-container uid read it
		return "", nil, fmt.Errorf("write gitconfig %s: %w", path, err)
	}
	cleanup := func() {
		_ = os.Remove(path)
	}
	return path, cleanup, nil
}

// wantsWorktreeMount returns true if the template's `mounts` field
// contains the string "worktree". Spec: specs/architecture.md
// §"Mount classes".
func wantsWorktreeMount(tpl templates.Template) bool {
	for _, m := range tpl.Mounts {
		if m == templates.MountClassWorktree {
			return true
		}
	}
	return false
}

// wantsSSHMount returns true if the template opts into the host
// ~/.ssh → /home/agent/.ssh read-only bind mount. Opt-in only: default
// templates never list "ssh". See
// plans/issues/feat-container-ssh-passthrough.md.
func wantsSSHMount(tpl templates.Template) bool {
	for _, m := range tpl.Mounts {
		if m == templates.MountClassSSH {
			return true
		}
	}
	return false
}

func containerName(agentName string, incID uuid.UUID) string {
	// "/" and ":" disallowed in Docker names; agentName is operator
	// input, so sanitize. We don't fail on malformed input — Docker
	// would emit a clearer error if our sanitizer let something
	// through, but the rules are well-known.
	safe := strings.Map(func(r rune) rune {
		switch {
		case r == '-' || r == '_' || r == '.':
			return r
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, agentName)
	if safe == "" {
		safe = "agent"
	}
	short := incID.String()
	if len(short) > 8 {
		short = short[:8]
	}
	return "sextant-" + safe + "-" + short
}

// permissionCeilingToSDKMode maps a sextant-internal permission_ceiling value
// (from the template TOML) to the Claude Agent SDK permissionMode string that
// the sidecar passes to sdkQuery. The mapping is:
//
//	"auto" or ""  → "acceptEdits"  (auto-accept Edit/Write; bash auto-gated)
//	"plan"        → "plan"
//
// "bypassPermissions" is never produced — it is prohibited by the
// [[sextant-permission-ceiling]] policy. Any unrecognized value falls
// back to "acceptEdits" so a stale or hand-edited template can't
// accidentally escalate privileges.
func permissionCeilingToSDKMode(ceiling string) string {
	switch ceiling {
	case "plan":
		return "plan"
	default:
		// Covers "auto", "" (unset), and any unrecognized value.
		return "acceptEdits"
	}
}

// ClaudeSeedVolumePrefix is the prefix for per-agent Docker named
// volumes that back the claude_seed copy-on-spawn flow. The full volume
// name is "<prefix><agent-uuid>". Stable per-agent so a
// restart-with-preserve-session reattaches the same volume and the
// SDK's session journal survives.
const ClaudeSeedVolumePrefix = "sextant-claude-seed-"

// ClaudeSeedVolumeName returns the canonical name of the per-agent
// claude_seed volume. Exported so the archive handler can compute the
// same name and delete the volume when an agent is archived.
func ClaudeSeedVolumeName(agentUUID uuid.UUID) string {
	return ClaudeSeedVolumePrefix + agentUUID.String()
}

// buildClaudeSeedMount returns the mount that should be appended to the
// container spec for a template with claude_seed set. The returned
// cleanup closure (nil for the readonly-bind mode) handles rollback —
// for copy-on-spawn, this means deleting a newly-created volume if a
// later step in the spawn fails. The cleanup is NOT registered for
// volumes that already existed (a restart of an agent with a prior
// session must not destroy that agent's working state on rollback).
//
// When deps.Volumes is nil and the mode is "copy-on-spawn", we fall
// back to the readonly-bind behavior so the spawn handler still works
// in test harnesses that didn't wire a volume manager. Production
// always wires deps.Volumes.
func buildClaudeSeedMount(ctx context.Context, deps SpawnDeps, mode, seedPath string, agentUUID uuid.UUID, image string) (containermgr.MountSpec, func(), error) {
	switch mode {
	case templates.ClaudeSeedModeReadonly:
		// Legacy bind-mount. No rollback cleanup needed — bind mounts
		// don't own host state.
		return containermgr.MountSpec{
			HostPath:      seedPath,
			ContainerPath: "/home/agent/.claude",
			ReadOnly:      true,
		}, nil, nil
	case templates.ClaudeSeedModeCopyOnSpawn, "":
		if deps.Volumes == nil {
			// No volume manager wired (unit-test fallback). Use the
			// readonly bind so tests that don't care about the copy
			// flow still get a deterministic mount shape.
			return containermgr.MountSpec{
				HostPath:      seedPath,
				ContainerPath: "/home/agent/.claude",
				ReadOnly:      true,
			}, nil, nil
		}
		volName := ClaudeSeedVolumeName(agentUUID)
		labels := map[string]string{
			LabelAgentUUID: agentUUID.String(),
			"sextant.kind": "claude-seed",
		}
		created, err := deps.Volumes.EnsureVolume(ctx, volName, labels)
		if err != nil {
			return containermgr.MountSpec{}, nil, fmt.Errorf("ensure volume %s: %w", volName, err)
		}
		if created {
			// First spawn for this agent UUID. Populate from the host
			// seed dir so the SDK boots with operator-curated content.
			// We always use the sidecar image (whatever the template
			// declares) for the populate one-shot — it's the image we
			// know is already pulled.
			if err := deps.Volumes.PopulateVolumeFromHostDir(ctx, volName, seedPath, image, nil); err != nil {
				// Populate failed: tear down the half-created volume so
				// the next spawn starts cleanly.
				_ = deps.Volumes.RemoveVolume(context.Background(), volName, true) //nolint:contextcheck // rollback uses a fresh ctx
				return containermgr.MountSpec{}, nil, fmt.Errorf("populate volume %s from %s: %w", volName, seedPath, err)
			}
		}
		var cleanup func()
		if created {
			// Only roll back the volume when we created it. Re-attaching
			// an existing volume is a no-op for the caller; deleting it
			// would destroy the agent's accumulated state. Rollback runs
			// after the outer spawn ctx may have been cancelled, so the
			// timeout context derives from Background by design.
			//nolint:contextcheck // rollback against fresh ctx is intentional
			cleanup = func() {
				rbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = deps.Volumes.RemoveVolume(rbCtx, volName, true)
			}
		}
		return containermgr.MountSpec{
			VolumeName:    volName,
			ContainerPath: "/home/agent/.claude",
			ReadOnly:      false,
		}, cleanup, nil
	default:
		return containermgr.MountSpec{}, nil, fmt.Errorf("unknown claude_seed_mode %q", mode)
	}
}

// insertDefinitionHistory writes one row into the agent_definitions_history
// ClickHouse table. The `definition` column is JSON — we pass the full
// AgentDefinition JSON so the history is self-describing.
func insertDefinitionHistory(ctx context.Context, hw HistoryWriter, def sextantproto.AgentDefinition, changeKind string) error {
	raw, err := json.Marshal(def)
	if err != nil {
		return fmt.Errorf("marshal definition: %w", err)
	}
	q := `INSERT INTO agent_definitions_history
		(agent_uuid, version, ts, actor, change_kind, definition)
		VALUES (?, ?, ?, ?, ?, ?)`
	return hw.Exec(ctx, q,
		def.UUID.String(),
		def.Version,
		def.UpdatedAt.Time,
		"operator",
		changeKind,
		string(raw),
	)
}
