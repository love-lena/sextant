package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/containermgr"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// LabelSpecFingerprint stamps a deterministic hash of the inputs the
// builder controls (image + ordered mounts + sorted env keys) onto
// every container the spec builder produces. It is the seed for P2
// drift detection (RFC §5.6): the reconciler recomputes the desired
// fingerprint from the persisted AgentDefinition (via this same
// builder) and compares it to the stamped label — a mismatch means the
// running container was built from a stale spec and must converge by
// restart. Kept local to the handlers package on purpose: the wire
// contract (pkg/sextantproto) is owned by a parallel workstream, so
// the fingerprint must not reach into it yet.
const LabelSpecFingerprint = "sextant.spec_fingerprint"

// LabelWireEpoch stamps the daemon's sextantproto.WireEpoch onto every
// container the spec builder produces — the runtime half of version-skew
// detection (RFC §5.8). The reconciler compares this label against the
// daemon's current WireEpoch; a container stamped with an older epoch was
// built by a since-upgraded daemon and is drift (RFC §5.6) → converge by
// restart at a turn boundary. The label lets this work even for an exited
// container (no live process to interrogate). Kept distinct from the spec
// fingerprint so an epoch bump alone (no image/mount/env delta) is still
// detected, and so the reason a restart fired stays legible.
const LabelWireEpoch = "sextant.wire_epoch"

// SidecarEntrypoint is the in-image path of the sidecar runtime's
// entrypoint script. The image's default CMD is /bin/bash (so the M9
// smoke test stays interactive); spawned agents always override it to
// run the long-lived sidecar runtime. Shared by the spec builder so
// spawn and restart can't drift on the launch command.
var SidecarEntrypoint = []string{"/opt/sextant/sidecar/entrypoint.sh"}

// agentContainerSpecInput is the complete, already-resolved set of
// inputs buildAgentContainerSpec projects into a containermgr.ContainerSpec.
//
// The spec is a pure projection of the persisted AgentDefinition plus
// the daemon's host-environment context: nothing here is conditional on
// "spawn vs restart". Legitimate spawn/restart differences are explicit
// fields — the freshly-minted IncarnationID, the per-incarnation JWT,
// and whether the SDK session id is being resumed — never the *absence*
// of a mount or an env var. That is the whole point of C0 (RFC §5.4):
// before this builder, restart silently omitted the gitconfig, SSH, and
// git-dir mounts because they only existed in spawn's inline code.
//
// Callers resolve the side-effecting host state (materialize the
// workspace, write the gitconfig file, ensure+populate the
// claude_seed volume) BEFORE calling the builder,
// because those steps own rollback ledgers that differ between spawn
// (create-and-roll-back) and restart (reattach-idempotently). The
// builder is pure: given identical resolved inputs it emits an
// identical spec.
type agentContainerSpecInput struct {
	// Def is the persisted AgentDefinition the spec projects from. The
	// image, env overlay, mount classes, model, permission ceiling and
	// initial prompt all read off it (or off the template at spawn time,
	// which clones into Def.Sandbox — so the restart projection is
	// lossless).
	Def sextantproto.AgentDefinition

	// IncarnationID is the per-incarnation identity. Minted fresh by
	// spawn and restart alike; this is the legitimate "modulo identity"
	// difference the acceptance bar allows.
	IncarnationID uuid.UUID

	// JWT is the per-incarnation token. Re-issued on every build.
	JWT string

	// HostID / NATSURL / NATSUser / NATSPassword / MCPURL / Issuer are
	// the daemon's environment context, identical across spawn/restart
	// for a given daemon process.
	HostID       string
	NATSURL      string
	NATSUser     string
	NATSPassword string
	MCPURL       string

	// Model is the resolved model (Def.Runtime.Model with the
	// DefaultModel fallback already applied by the caller).
	Model string

	// SessionID, when non-empty, resumes a prior SDK session. Spawn sets
	// it from Def.Runtime.SessionID; restart sets it only when
	// --preserve-session is true. The session-resume decision is a
	// legitimate per-build difference, so it is an explicit input.
	SessionID string

	// APIKey, when non-empty, becomes ANTHROPIC_API_KEY. Empty falls
	// back to the SDK's default credential chain.
	APIKey string

	// TestRunLabel, when non-empty, stamps sextant.test_run for
	// test-scoped cleanup. Empty in production.
	TestRunLabel string

	// --- resolved mount sources (host paths the caller materialized) ---

	// WorkspacePath is the host path bind-mounted at /workspace. Always
	// present.
	WorkspacePath string

	// GitDirHostPath, when non-empty, is the host <repo>/.git directory
	// bind-mounted at the same path inside the container so a worktree's
	// `.git` pointer file resolves. Set only when the workspace is a
	// worktree AND the daemon knows the repo root.
	GitDirHostPath string

	// GitConfigHostPath, when non-empty, is the per-agent gitconfig file
	// bind-mounted read-only at /home/agent/.gitconfig.
	GitConfigHostPath string

	// SSHHostPath, when non-empty, is the host ~/.ssh dir bind-mounted
	// read-only at /home/agent/.ssh. Set only when the template opts in
	// via the "ssh" mount class.
	SSHHostPath string

	// ClaudeSeedMount, when non-zero, is the resolved /home/agent/.claude
	// seed mount (a named volume in copy-on-spawn mode, a read-only bind
	// in readonly-bind mode). The caller builds it via buildClaudeSeedMount
	// so the volume side effects + rollback stay caller-owned.
	ClaudeSeedMount *containermgr.MountSpec
}

// buildAgentContainerSpec is the SOLE builder of an agent's container
// spec. Both spawn_agent and restart_agent route through it so the two
// paths cannot drift — the class of bug where restart dropped the
// gitconfig/SSH/git-dir mounts (RFC §10.3, the #50 family) is gone by
// construction, not merely de-duplicated.
//
// The returned spec carries a spec-fingerprint label (LabelSpecFingerprint)
// computed from the image, the ordered mount targets, and the sorted env
// keys — the deterministic seed for P2 drift detection.
func buildAgentContainerSpec(in agentContainerSpecInput) containermgr.ContainerSpec {
	def := in.Def

	env := buildContainerEnv(containerEnvInput{
		AgentUUID:      def.UUID,
		AgentName:      def.Name,
		IncarnationID:  in.IncarnationID,
		HostID:         in.HostID,
		NATSURL:        in.NATSURL,
		NATSUser:       in.NATSUser,
		NATSPassword:   in.NATSPassword,
		JWT:            in.JWT,
		MCPURL:         in.MCPURL,
		Model:          in.Model,
		PermissionMode: permissionCeilingToSDKMode(def.Spec.Runtime.PermissionCeil),
		APIKey:         in.APIKey,
		SessionID:      in.SessionID,
		InitialPrompt:  def.Spec.Runtime.InitialPrompt,
		EnvOverlay:     def.Spec.Sandbox.Env,
	})

	// Mount assembly. The ORDER is fixed and shared so spawn and restart
	// emit byte-identical mount slices for identical inputs. Each mount
	// is gated on its resolved source being present — absence means the
	// agent's definition doesn't request it, NOT that a code path forgot
	// to add it.
	//
	//  1. workspace      — always.
	//  2. git-dir        — worktree workspaces with a known repo root.
	//  3. gitconfig (ro) — every agent (git identity for commits).
	//  4. ssh (ro)       — template opt-in ("ssh" mount class).
	//  5. claude_seed    — template-declared /home/agent/.claude seed.
	//
	// The persistent claude-projects bind-mount was REMOVED (S0, RFC
	// §5.10): the SDK session JSONL stays in-container ground-truth, read
	// on demand via read_file and snapshotted on stop. Removing it kills
	// the #49/#50 mount-drift class at the root — there is no longer a
	// mount restart must remember.
	mounts := []containermgr.MountSpec{
		{HostPath: in.WorkspacePath, ContainerPath: WorkspaceMountPath},
	}
	if in.GitDirHostPath != "" {
		mounts = append(mounts, containermgr.MountSpec{
			HostPath:      in.GitDirHostPath,
			ContainerPath: in.GitDirHostPath,
		})
	}
	if in.GitConfigHostPath != "" {
		mounts = append(mounts, containermgr.MountSpec{
			HostPath:      in.GitConfigHostPath,
			ContainerPath: "/home/agent/.gitconfig",
			ReadOnly:      true,
		})
	}
	if in.SSHHostPath != "" {
		mounts = append(mounts, containermgr.MountSpec{
			HostPath:      in.SSHHostPath,
			ContainerPath: "/home/agent/.ssh",
			ReadOnly:      true,
		})
	}
	if in.ClaudeSeedMount != nil {
		mounts = append(mounts, *in.ClaudeSeedMount)
	}

	labels := map[string]string{
		LabelAgentUUID:     def.UUID.String(),
		LabelAgentName:     def.Name,
		LabelHostID:        in.HostID,
		LabelIncarnationID: in.IncarnationID.String(),
		LabelTemplate:      def.Template,
	}
	if in.TestRunLabel != "" {
		labels[LabelTestRun] = in.TestRunLabel
	}
	labels[LabelSpecFingerprint] = specFingerprint(def.Spec.Sandbox.Image, mounts, env)
	labels[LabelWireEpoch] = strconv.Itoa(sextantproto.WireEpoch)

	return containermgr.ContainerSpec{
		Name:       containerName(def.Name, in.IncarnationID),
		Image:      def.Spec.Sandbox.Image,
		Cmd:        append([]string(nil), SidecarEntrypoint...),
		Env:        env,
		Mounts:     mounts,
		Labels:     labels,
		AutoRemove: true,
	}
}

// specFingerprint hashes the inputs the builder controls into a stable,
// order-independent-where-it-should-be digest (RFC §5.6). It folds in:
//
//   - the image reference,
//   - the ordered mount *targets* (container path + ro flag + whether the
//     source is a named volume vs a bind) — the host source path is
//     deliberately excluded because it carries identity (per-agent UUID
//     dirs, the incarnation-independent repo root) that must NOT change
//     the fingerprint, while the container-side shape is the thing drift
//     detection cares about,
//   - the sorted set of env *keys* — keys, not values, because values
//     carry per-incarnation identity (JWT, incarnation id) that would
//     otherwise make every restart look like drift.
//
// The result is hex(sha256(...)) so it is a safe Docker label value.
func specFingerprint(image string, mounts []containermgr.MountSpec, env map[string]string) string {
	// Build the canonical pre-image as a single string, then hash once.
	// strings.Builder.Write* never errors, so no error handling noise —
	// and it keeps the digest input trivially inspectable in tests.
	var b strings.Builder
	b.WriteString("image\x00")
	b.WriteString(image)
	b.WriteString("\x00")

	// Mounts in declared order (order is part of the spec's identity:
	// Docker resolves deepest-prefix per path, so re-ordering two mounts
	// that overlap could change behavior).
	b.WriteString("mounts\x00")
	for _, m := range mounts {
		kind := "bind"
		if m.VolumeName != "" && m.HostPath == "" {
			kind = "volume"
		}
		ro := "rw"
		if m.ReadOnly {
			ro = "ro"
		}
		b.WriteString(kind)
		b.WriteString("\x00")
		b.WriteString(m.ContainerPath)
		b.WriteString("\x00")
		b.WriteString(ro)
		b.WriteString("\x00")
	}

	// Env keys, sorted (map iteration order is non-deterministic; only
	// the key *set* is part of the spec contract).
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b.WriteString("env\x00")
	b.WriteString(strings.Join(keys, "\x00"))

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
