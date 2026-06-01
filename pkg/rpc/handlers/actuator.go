package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/authjwt"
	"github.com/love-lena/sextant/pkg/containermgr"
	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/templates"
	"github.com/love-lena/sextant/pkg/worktree"
)

// DockerOpTimeout caps a SINGLE external runtime operation the actuator
// performs (container run, file-copy snapshot, volume reclaim) — the
// fail-early budget so a wedged dockerd surfaces a loud `context deadline
// exceeded` instead of blocking the single reconcile worker forever
// (bug-ctl-reconcile-loop-stalls-under-sustained-recovery-churn). Each op
// is wrapped with a deadline DERIVED FROM the passed reconcile ctx (never
// context.Background), so a daemon shutdown still cancels it promptly.
//
// Stop is the one op exempt from this flat cap: a SIGTERM→SIGKILL stop
// legitimately takes up to the agent's grace, so its deadline is
// grace + DockerStopGraceBuffer (see graceDeadline).
const DockerOpTimeout = 30 * time.Second

// DockerStopGraceBuffer is the headroom added on top of the SIGTERM grace
// when bounding a container Stop: dockerd needs a little slack past the
// grace to deliver SIGKILL and remove the container, so the deadline is
// grace + this buffer rather than the flat DockerOpTimeout.
const DockerStopGraceBuffer = 15 * time.Second

// opTimeout resolves the per-operation deadline (deps override or the
// DockerOpTimeout default). stopGraceBuffer resolves the Stop grace
// headroom (deps override or DockerStopGraceBuffer).
func (a *Actuator) opTimeout() time.Duration {
	if a.deps.DockerOpTimeout > 0 {
		return a.deps.DockerOpTimeout
	}
	return DockerOpTimeout
}

func (a *Actuator) stopGraceBuffer() time.Duration {
	if a.deps.DockerStopGraceBuffer > 0 {
		return a.deps.DockerStopGraceBuffer
	}
	return DockerStopGraceBuffer
}

// boundedOp derives a child context capped at the op timeout from the
// passed (reconcile) ctx. Cancellation still propagates — a daemon
// shutdown cancels the parent, which cancels this — so the wrapper only
// ever SHORTENS the deadline, never detaches from cancellation.
func (a *Actuator) boundedOp(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, a.opTimeout())
}

// boundedStop derives a child context for a container Stop, whose deadline
// is grace + the stop grace buffer (a SIGTERM→SIGKILL stop legitimately
// runs the full grace). Derived from the passed ctx so shutdown still
// cancels it.
func (a *Actuator) boundedStop(ctx context.Context, grace time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, grace+a.stopGraceBuffer())
}

// ContainerProjectsDir is the in-container base where the Claude Code SDK
// writes its per-session journal: <ContainerProjectsDir>/<encoded-cwd>/
// <sessionId>.jsonl. With the persistent claude-projects bind-mount gone
// (S0, RFC §5.10) this path is read on demand (read_file) and snapshotted
// on stop; it is no longer a host bind target.
const ContainerProjectsDir = "/home/agent/.claude/projects"

// containerCWDEncoded is the SDK's encoding of the sidecar working
// directory (/workspace, set by the image's WORKDIR). Claude Code encodes
// a cwd into the projects-dir segment by replacing every "/" with "-", so
// "/workspace" → "-workspace". The sidecar always runs the SDK from
// /workspace, so this segment is deterministic and shared by the
// snapshot + on-demand read paths.
const containerCWDEncoded = "-workspace"

// ContainerSessionJSONLPath returns the deterministic in-container path of
// the session JSONL for sessionID. Empty sessionID returns "" (no turn has
// flushed a session yet).
func ContainerSessionJSONLPath(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" {
		return ""
	}
	return ContainerProjectsDir + "/" + containerCWDEncoded + "/" + sessionID + ".jsonl"
}

// SessionSnapshotPath returns the host path of the durable session-log
// snapshot for an agent (<root>/<uuid>/session-snapshot.jsonl). Empty root
// returns "" — snapshotting is disabled when the daemon has no data root.
func SessionSnapshotPath(root string, agentUUID uuid.UUID) string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, agentUUID.String(), "session-snapshot.jsonl")
}

// snapshotSessionLog copies the authoritative in-container session JSONL
// to the durable host snapshot (S0, RFC §5.10). Best-effort throughout:
// every failure is logged and swallowed so a missing snapshot never blocks
// a stop/teardown. A snapshot exists so an operator can still read the
// transcript after the (AutoRemove) container is gone (agents context
// --backup falls back to it).
func (a *Actuator) snapshotSessionLog(ctx context.Context, def sextantproto.AgentDefinition, containerID string) {
	if a.deps.SnapshotCopier == nil || a.deps.AgentsDataRoot == "" {
		return
	}
	if def.Spec.Runtime.SessionID == nil || strings.TrimSpace(*def.Spec.Runtime.SessionID) == "" {
		// No SDK session was ever recorded — nothing to snapshot (the agent
		// never completed a turn). Not an error.
		return
	}
	srcPath := ContainerSessionJSONLPath(*def.Spec.Runtime.SessionID)
	// Bound the copy: a wedged dockerd must not let a best-effort snapshot
	// block the stop/teardown that drives the reconcile worker.
	cctx, cancel := a.boundedOp(ctx)
	defer cancel()
	data, err := a.deps.SnapshotCopier.CopyFileFromContainer(cctx, containerID, srcPath)
	if err != nil {
		if errors.Is(err, containermgr.ErrPathNotFound) {
			// The session id is recorded but the JSONL isn't where we expect
			// (a different cwd encoding, or never flushed). Soft skip.
			return
		}
		log.Printf("sextantd: snapshot session log for %s: %v", def.UUID, err)
		return
	}
	dst := SessionSnapshotPath(a.deps.AgentsDataRoot, def.UUID)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		log.Printf("sextantd: snapshot session log for %s: mkdir: %v", def.UUID, err)
		return
	}
	// 0o600: the transcript may contain prompt content; keep it operator-only.
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		log.Printf("sextantd: snapshot session log for %s: write %s: %v", def.UUID, dst, err)
	}
}

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
	Definitions  AgentMutableKV
	Incarnations AgentMutableKV
	Templates    templates.KV
	Containers   ContainerRunner
	// SnapshotCopier is the copy-from-container surface the snapshot-on-stop
	// path uses (S0, RFC §5.10). Optional: nil disables snapshotting (most
	// unit tests). The real daemon wires *containermgr.Manager, which copies
	// the session JSONL out of an already-stopped container.
	SnapshotCopier ContainerFileCopier
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
	// DockerOpTimeout / DockerStopGraceBuffer override the per-operation
	// fail-early deadlines (defaults: the package DockerOpTimeout /
	// DockerStopGraceBuffer constants). Tests shrink these to prove a wedged
	// runtime op surfaces a deadline error promptly without waiting the real
	// 30s; production leaves them zero (defaults apply).
	DockerOpTimeout       time.Duration
	DockerStopGraceBuffer time.Duration
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
//     claude_seed volume) — the lossless projection.
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
		if old.ContainerID != "" {
			// Snapshot the outgoing incarnation's session log before stopping
			// it — a re-actuation (restart/recovery) also leaves running, so
			// the durable transcript must be captured here too (S0, RFC §5.10).
			a.snapshotSessionLog(ctx, def, old.ContainerID)
			if a.deps.Containers != nil {
				grace := a.graceFor(def)
				sctx, scancel := a.boundedStop(ctx, grace)
				if serr := a.deps.Containers.Stop(sctx, old.ContainerID, grace); serr != nil {
					// Best-effort (a fresh incarnation supersedes it regardless),
					// but a timeout here is a loud signal that dockerd is wedged —
					// log it with the agent + op so a stall is observable.
					log.Printf("sextantd: actuate %s: stop prior incarnation %s: %v",
						def.UUID, old.ContainerID, serr)
				}
				scancel()
			}
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
	// (workspace, gitconfig, fresh claude_seed volume)
	// in reverse order — so a failed actuation leaks nothing on the host.
	specIn, rollback, err := a.buildSpecInput(ctx, def, incID, jwt, resumeSession)
	if err != nil {
		rollback()
		return ActuateResult{}, err
	}

	// 4. Build + run. Bound the run so a wedged dockerd surfaces a loud
	// deadline error instead of blocking the single reconcile worker.
	spec := buildAgentContainerSpec(specIn)
	rctx, rcancel := a.boundedOp(ctx)
	container, err := a.deps.Containers.Run(rctx, spec)
	rcancel()
	if err != nil {
		rollback()
		if errors.Is(err, context.DeadlineExceeded) {
			log.Printf("sextantd: actuate %s: run container timed out after %s (dockerd wedged?)",
				def.UUID, a.opTimeout())
		}
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
			//nolint:contextcheck // rollback against fresh ctx is intentional: the request ctx may be cancelled
			stopRollback := func() {
				rbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = a.deps.Containers.Stop(rbCtx, container.ID, 5*time.Second)
			}
			stopRollback()
		}
		rollback()
		return ActuateResult{}, fmt.Errorf("actuate: persist incarnation: %w", err)
	}

	return ActuateResult{IncarnationID: incID, ContainerID: container.ID}, nil
}

// DesiredSpecID is the C0-builder-derived identity of the spec the
// reconciler WOULD build for def right now — the desired half of the P2
// drift compare (RFC §5.6, §5.8). The reconciler compares it against the
// labels stamped on the RUNNING container; any mismatch is drift.
//
// Fingerprint is recomputed via the SAME buildAgentContainerSpec path the
// actuation uses (RFC §5.6: "recompute the desired fingerprint from the
// AgentDefinition via the same builder"). This is what avoids
// false-positives: we diff OUR builder's output, never docker's
// normalized/injected live spec. WireEpoch is the daemon's current epoch
// (RFC §5.8); a running container stamped with an older epoch is drift.
type DesiredSpecID struct {
	Fingerprint string
	WireEpoch   int
}

// DesiredFingerprint recomputes the desired spec identity for def via the
// C0 single-source builder (RFC §5.6). It resolves the same host state an
// actuation would (the projection must match byte-for-byte to compare
// against the stamped label) but does NOT run a container, mint a usable
// JWT, or write status — it is a pure read used by the drift branch.
//
// Drift is only evaluated for a RUNNING container, so the host artifacts
// (workspace/worktree, gitconfig, projects dir, seed volume) already exist
// and buildSpecInput's resolution is idempotent reattachment — it
// materializes nothing new here. The placeholder JWT only contributes the
// (always-present) SEXTANT_JWT env KEY, which is all the fingerprint folds
// in; the token VALUE is deliberately excluded from the fingerprint.
func (a *Actuator) DesiredFingerprint(ctx context.Context, def sextantproto.AgentDefinition) (DesiredSpecID, error) {
	// Use the agent's recorded session-resume disposition so the recomputed
	// env-key set matches what the live incarnation was built with (the
	// SEXTANT_SESSION_ID env key is gated on it). resumeSessionFor mirrors
	// the reconciler's actuation call.
	resume := def.Spec.Runtime.SessionID != nil
	specIn, _, err := a.buildSpecInput(ctx, def, def.Status.CurrentIncarnationID, "fingerprint-probe", resume)
	if err != nil {
		return DesiredSpecID{}, fmt.Errorf("desired fingerprint: build spec input: %w", err)
	}
	spec := buildAgentContainerSpec(specIn)
	return DesiredSpecID{
		Fingerprint: spec.Labels[LabelSpecFingerprint],
		WireEpoch:   sextantproto.WireEpoch,
	}, nil
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
	if old.ContainerID != "" {
		// Durable snapshot-on-stop (S0, RFC §5.10): capture the
		// authoritative in-container session JSONL into the agent data dir
		// BEFORE issuing the stop. The agent containers run with
		// AutoRemove=true, so once Stop's grace elapses the container (and
		// its filesystem layer) is gone — copying first, while the layer is
		// still readable, sidesteps the removal race and still captures the
		// flushed-at-turn-boundary transcript. Best-effort: a failed snapshot
		// is an observability gap, never a stop blocker.
		a.snapshotSessionLog(ctx, def, old.ContainerID)
		if a.deps.Containers != nil {
			grace := a.graceFor(def)
			sctx, scancel := a.boundedStop(ctx, grace)
			err := a.deps.Containers.Stop(sctx, old.ContainerID, grace)
			scancel()
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					log.Printf("sextantd: stop %s: stop container %s timed out after %s (dockerd wedged?)",
						def.UUID, old.ContainerID, grace+a.stopGraceBuffer())
				}
				return fmt.Errorf("stop: stop container %s: %w", old.ContainerID, err)
			}
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
// the incarnation exited, and RECLAIM the per-agent claude_seed volume.
// It is the FINALIZER body (bug-ctl-archive-volume-leak): a volume-remove
// FAILURE is returned, NOT swallowed to stderr — so the reconciler leaves
// the agent in observed=archiving (intermediate, name still held) and
// retries the reclaim next pass, rather than finalizing a "reclaimed"
// record over a leaked volume. The name release is gated on the TERMINAL
// observed=archived state the reconciler only reaches once this returns
// nil (AgentDefinition.NameReleased), so Teardown owning the reclaim is
// what keeps the name held until cleanup is confirmed.
//
// Idempotent: Stop is a no-op without a live incarnation, and RemoveVolume
// treats an already-gone volume as success — so a retry after a partial
// teardown converges (the volume reclaim is the only step that can fail,
// and once it is gone the next pass returns nil and the reconciler
// finalizes).
func (a *Actuator) Teardown(ctx context.Context, def sextantproto.AgentDefinition) error {
	if err := a.Stop(ctx, def); err != nil {
		return err
	}
	if a.deps.Volumes != nil {
		volName := ClaudeSeedVolumeName(def.UUID)
		vctx, vcancel := a.boundedOp(ctx)
		err := a.deps.Volumes.RemoveVolume(vctx, volName, true)
		vcancel()
		if err != nil {
			// NOT best-effort: an unreclaimed volume must keep the agent in
			// archiving so the reconciler retries — never finalize over a leak.
			if errors.Is(err, context.DeadlineExceeded) {
				log.Printf("sextantd: teardown %s: reclaim volume %s timed out after %s (dockerd wedged?)",
					def.UUID, volName, a.opTimeout())
			}
			return fmt.Errorf("teardown: reclaim volume %s: %w", volName, err)
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
	// NOTE: the persistent claude-projects bind-mount is gone (S0, RFC
	// §5.10). The SDK session JSONL stays in-container ground-truth; it is
	// read on demand (read_file) and snapshotted to the agent data dir when
	// the reconciler observes the agent leave running (snapshotSessionLog).
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
