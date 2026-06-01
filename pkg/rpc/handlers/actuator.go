package handlers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/authjwt"
	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/templates"
	"github.com/love-lena/sextant/pkg/worktree"
)

// ActuatorDeps is the dependency bundle the reconciler hands the
// Actuator. It is the union of the host-environment context plus the
// container runtime + KV surfaces the actuation path needs. The daemon
// wires it once; the reconciler is its only caller (RFC §5: the sole
// actuator).
//
// This is deliberately the same shape SpawnDeps carried — the actuator
// is the spawn/restart bodies, relocated behind the reconcile loop so
// there is exactly one path that builds-and-runs a container.
type ActuatorDeps struct {
	Definitions    AgentMutableKV
	Incarnations   AgentMutableKV
	Templates      templates.KV
	Containers     ContainerRunner
	Volumes        VolumeManager
	CA             *authjwt.CA
	History        HistoryWriter
	WorkspaceRoot  string
	AgentsDataRoot string
	Worktree       WorktreeProvider
	RepoRoot       string
	HostID         string
	NATSURL        string
	NATSUser       string
	NATSPassword   string
	MCPURL         string
	Issuer         string
	TestRunLabel   string
	Now            func() time.Time
}

// Actuator is the sole actuator (RFC §5). It is the only thing in the
// daemon that calls Containers.Run / Containers.Stop — handlers write
// desired state to KV and the reconciler drives this. Every method is
// idempotent in the level-triggered sense: "ensure," not "do."
type Actuator struct {
	deps ActuatorDeps
}

// NewActuator returns an Actuator. The Now hook defaults to time.Now.
func NewActuator(deps ActuatorDeps) *Actuator {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Actuator{deps: deps}
}

// ActuateResult reports what Actuate did so the reconciler can write the
// observed status back (the reconciler is the SOLE writer of status).
type ActuateResult struct {
	// IncarnationID is the fresh incarnation the actuation created.
	IncarnationID uuid.UUID
	// ContainerID is the container Docker handed back.
	ContainerID string
}

// Actuate builds and runs a fresh incarnation for def, replacing any
// prior live incarnation. This is the merged spawn+restart body (RFC
// §5.4: one actuation path, no spawn/restart drift). It:
//
//  1. Stops + marks-exited the prior live incarnation (if any).
//  2. Resolves the per-incarnation host state (workspace, gitconfig,
//     claude-projects dir, claude_seed volume) — the lossless projection.
//  3. Mints a fresh per-incarnation JWT.
//  4. Builds the spec via the C0 single-source builder and Runs it.
//  5. Persists the new AgentIncarnation (state=starting).
//
// It does NOT write the AgentDefinition's status — the reconciler owns
// that. The returned ActuateResult carries the identity the reconciler
// stamps into status.
//
// resumeSession requests the SDK session be resumed (restart
// --preserve-session); spawn always resumes when the def already records
// a session id, which this honors automatically when resumeSession is
// true OR the def has a recorded session.
func (a *Actuator) Actuate(ctx context.Context, def sextantproto.AgentDefinition, resumeSession bool) (ActuateResult, error) {
	now := a.deps.Now().UTC()

	// 1. Stop the prior live incarnation, if any. Best-effort — a fresh
	// incarnation supersedes it regardless.
	if old, oldKey, err := findLiveIncarnation(ctx, a.deps.Incarnations, def.UUID); err == nil && old != nil {
		if old.ContainerID != "" && a.deps.Containers != nil {
			_ = a.deps.Containers.Stop(ctx, old.ContainerID, a.graceFor(def))
		}
		ended := sextantproto.AtTimestamp(now)
		old.State = sextantproto.IncarnationExited
		old.EndedAt = &ended
		_ = putJSON(ctx, a.deps.Incarnations, oldKey, *old)
	}

	incID := uuid.New()

	// 3. Mint the per-incarnation JWT.
	if a.deps.CA == nil {
		return ActuateResult{}, fmt.Errorf("actuate: CA is nil")
	}
	jwt, err := a.deps.CA.Issue(authjwt.Claims{
		AgentUUID:     def.UUID,
		IncarnationID: incID,
		Capabilities:  append([]string(nil), def.Spec.Tools...),
		IssuedAt:      now,
		ExpiresAt:     now.Add(SpawnJWTLifetime),
		Issuer:        a.deps.Issuer,
	})
	if err != nil {
		return ActuateResult{}, fmt.Errorf("actuate: issue jwt: %w", err)
	}

	// 2. Resolve host state + build the spec input. rollback fires the
	// cleanup closures for every host-side artifact this actuation created
	// (workspace, gitconfig, claude-projects dir, fresh claude_seed volume)
	// in reverse order — so a failed actuation leaks nothing on the host.
	specIn, rollback, err := a.buildSpecInput(ctx, def, incID, jwt, resumeSession)
	if err != nil {
		rollback()
		return ActuateResult{}, err
	}

	// 4. Build + run.
	spec := buildAgentContainerSpec(specIn)
	container, err := a.deps.Containers.Run(ctx, spec)
	if err != nil {
		rollback()
		return ActuateResult{}, fmt.Errorf("actuate: run container: %w", err)
	}

	// 5. Persist the new incarnation.
	inc := sextantproto.AgentIncarnation{
		IncarnationID: incID,
		AgentUUID:     def.UUID,
		ContainerID:   container.ID,
		StartedAt:     sextantproto.AtTimestamp(now),
		HostID:        a.deps.HostID,
		State:         sextantproto.IncarnationStarting,
	}
	if err := putJSON(ctx, a.deps.Incarnations, incID.String(), inc); err != nil {
		// Roll the container back so we don't leak it behind a missing
		// incarnation record, then unwind the host-side artifacts. The
		// reconciler will re-actuate next pass.
		if a.deps.Containers != nil {
			rbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = a.deps.Containers.Stop(rbCtx, container.ID, 5*time.Second)
			cancel()
		}
		rollback()
		return ActuateResult{}, fmt.Errorf("actuate: persist incarnation: %w", err)
	}

	return ActuateResult{IncarnationID: incID, ContainerID: container.ID}, nil
}

// Stop stops the agent's live container (the paused-intent action). The
// record + name are retained. Idempotent: no live incarnation is a
// no-op.
func (a *Actuator) Stop(ctx context.Context, def sextantproto.AgentDefinition) error {
	old, oldKey, err := findLiveIncarnation(ctx, a.deps.Incarnations, def.UUID)
	if err != nil {
		return fmt.Errorf("stop: find live incarnation: %w", err)
	}
	if old == nil {
		return nil
	}
	if old.ContainerID != "" && a.deps.Containers != nil {
		if err := a.deps.Containers.Stop(ctx, old.ContainerID, a.graceFor(def)); err != nil {
			return fmt.Errorf("stop: stop container %s: %w", old.ContainerID, err)
		}
	}
	ended := sextantproto.AtTimestamp(a.deps.Now().UTC())
	old.State = sextantproto.IncarnationExited
	old.EndedAt = &ended
	if err := putJSON(ctx, a.deps.Incarnations, oldKey, *old); err != nil {
		return fmt.Errorf("stop: mark incarnation exited: %w", err)
	}
	return nil
}

// Teardown is the archived-intent action: stop any live container, mark
// the incarnation exited, and release the per-agent claude_seed volume.
// The name release is a property of the desired=archived record
// (agentNameInUse skips archived defs), so Teardown only owns the
// runtime side effects. Idempotent.
func (a *Actuator) Teardown(ctx context.Context, def sextantproto.AgentDefinition) error {
	if err := a.Stop(ctx, def); err != nil {
		return err
	}
	if a.deps.Volumes != nil {
		volName := ClaudeSeedVolumeName(def.UUID)
		if err := a.deps.Volumes.RemoveVolume(ctx, volName, true); err != nil {
			// Best-effort: a failed volume remove is an operator chore, not
			// a teardown blocker (matches the legacy archive handler).
			fmt.Fprintf(os.Stderr, "actuate teardown: remove volume %s: %v\n", volName, err)
		}
	}
	return nil
}

// graceFor resolves the SIGTERM→SIGKILL grace for def. Spec override
// wins; otherwise the daemon default (RFC §8: 30s baseline).
func (a *Actuator) graceFor(def sextantproto.AgentDefinition) time.Duration {
	if def.Spec.GraceSeconds > 0 {
		return time.Duration(def.Spec.GraceSeconds) * time.Second
	}
	return defaultGraceSeconds * time.Second
}

// buildSpecInput resolves the per-incarnation host state and assembles
// the agentContainerSpecInput. This is the merged spawn+restart
// materialization, so the two paths cannot drift (RFC §5.4). All mounts
// are projected from the persisted def.Spec — never conditional on
// "spawn vs restart."
func (a *Actuator) buildSpecInput(ctx context.Context, def sextantproto.AgentDefinition, incID uuid.UUID, jwt string, resumeSession bool) (agentContainerSpecInput, func(), error) {
	// cleanups collects the host-side artifacts this actuation creates so a
	// failed actuation can unwind them (no orphaned dirs/volumes on the
	// host). rollback fires them in reverse order. The reconciler re-actuates
	// next pass; every create here is idempotent so unwinding between failed
	// attempts is harmless.
	var cleanups []func()
	rollback := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			if cleanups[i] != nil {
				cleanups[i]()
			}
		}
	}
	fail := func(err error) (agentContainerSpecInput, func(), error) {
		rollback()
		return agentContainerSpecInput{}, func() {}, err
	}

	model := def.Spec.Runtime.Model
	if strings.TrimSpace(model) == "" {
		model = DefaultModel
	}

	var sessionID string
	if (resumeSession || def.Status.CurrentIncarnationID == uuid.Nil) && def.Spec.Runtime.SessionID != nil {
		// Resume when the operator asked (restart --preserve-session) or on
		// the initial actuation if a session was recorded (spawn semantics).
		sessionID = *def.Spec.Runtime.SessionID
	}

	// Workspace: a worktree-class agent re-mounts the SAME worktree
	// (resolved by the deterministic spawn-worktree name; created on first
	// actuation), everything else uses the per-UUID stop-gap dir. The
	// stop-gap dir is this actuation's to clean up on failure; a worktree's
	// lifecycle belongs to the reconciler/GC, so it is not unwound here.
	workspace, usingWorktree, err := a.resolveWorkspace(ctx, def)
	if err != nil {
		return fail(err)
	}
	if !usingWorktree {
		ws := workspace
		cleanups = append(cleanups, func() { _ = os.RemoveAll(ws) })
	}

	// Per-agent gitconfig — idempotent (identical content each incarnation).
	gitconfigPath, gitconfigCleanup, err := writeAgentGitConfig(a.deps.WorkspaceRoot, def.UUID, def.Name)
	if err != nil {
		return fail(fmt.Errorf("actuate: write gitconfig: %w", err))
	}
	cleanups = append(cleanups, gitconfigCleanup)

	specIn := agentContainerSpecInput{
		Def:               def,
		IncarnationID:     incID,
		JWT:               jwt,
		HostID:            a.deps.HostID,
		NATSURL:           a.deps.NATSURL,
		NATSUser:          a.deps.NATSUser,
		NATSPassword:      a.deps.NATSPassword,
		MCPURL:            a.deps.MCPURL,
		Model:             model,
		SessionID:         sessionID,
		APIKey:            hostAPIKey(),
		TestRunLabel:      a.deps.TestRunLabel,
		WorkspacePath:     workspace,
		GitConfigHostPath: gitconfigPath,
	}
	if usingWorktree && a.deps.RepoRoot != "" {
		specIn.GitDirHostPath = filepath.Join(a.deps.RepoRoot, ".git")
	}
	if mountClassListed(def.Spec.Sandbox.Mounts, templates.MountClassSSH) {
		home, err := os.UserHomeDir()
		if err != nil {
			return fail(fmt.Errorf("actuate: resolve home for ssh mount: %w", err))
		}
		specIn.SSHHostPath = filepath.Join(home, ".ssh")
	}
	// claude_seed mount (per-agent named volume, populated on first
	// actuation, idempotent thereafter). The cleanup removes the volume only
	// when THIS actuation created it.
	if a.deps.Templates != nil && def.Template != "" {
		if tpl, terr := templates.LoadFromKV(ctx, a.deps.Templates, def.Template); terr == nil && tpl.ClaudeSeed != "" {
			if seedPath, eerr := templates.ExpandClaudeSeed(tpl.ClaudeSeed); eerr == nil {
				seedMount, seedCleanup, serr := buildClaudeSeedMount(ctx, SpawnDeps{Volumes: a.deps.Volumes}, tpl.ResolveClaudeSeedMode(), seedPath, def.UUID, def.Spec.Sandbox.Image)
				if serr == nil {
					specIn.ClaudeSeedMount = &seedMount
					cleanups = append(cleanups, seedCleanup)
				}
			}
		}
	}
	// Per-agent claude-projects bind-mount.
	if a.deps.AgentsDataRoot != "" {
		if projectsHost, projectsCleanup, perr := ensureAgentProjectsDir(a.deps.AgentsDataRoot, def.UUID); perr == nil {
			specIn.ClaudeProjectsHostPath = projectsHost
			cleanups = append(cleanups, projectsCleanup)
		}
	}
	return specIn, rollback, nil
}

// resolveWorkspace returns the /workspace host path + whether it is a
// worktree. Worktree-class agents resolve the spawn-named worktree if it
// exists, else create it; everything else uses the per-UUID stop-gap dir.
// Merging spawn's Create with restart's Resolve into one "ensure" keeps
// the actuation idempotent across incarnations.
func (a *Actuator) resolveWorkspace(ctx context.Context, def sextantproto.AgentDefinition) (string, bool, error) {
	if a.deps.Worktree != nil && mountClassListed(def.Spec.Sandbox.Mounts, templates.MountClassWorktree) {
		name := worktree.SpawnWorktreeName(def.Template, def.UUID)
		path, ok, err := a.deps.Worktree.Resolve(ctx, name)
		if err != nil {
			return "", false, fmt.Errorf("actuate: resolve worktree %s: %w", name, err)
		}
		if ok {
			return path, true, nil
		}
		// Not yet created (first actuation) — create it.
		info, cerr := a.deps.Worktree.Create(ctx, name, "main", def.UUID)
		if cerr == nil {
			return info.Path, true, nil
		}
		// Create failed (e.g. surface disabled) — fall through to stop-gap.
	}
	path, err := ensureWorkspaceDir(a.deps.WorkspaceRoot, def.UUID.String())
	if err != nil {
		return "", false, err
	}
	return path, false, nil
}

// LiveIncarnationContainerID returns the container id of the agent's
// current live incarnation, or "" when there is none. The reconciler
// uses it to re-observe actual container reality (the "actual" half of
// level reconciliation).
func LiveIncarnationContainerID(ctx context.Context, incs AgentMutableKV, agent uuid.UUID) (string, error) {
	inc, _, err := findLiveIncarnation(ctx, incs, agent)
	if err != nil {
		return "", err
	}
	if inc == nil {
		return "", nil
	}
	return inc.ContainerID, nil
}
