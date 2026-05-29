package handlers

import (
	"testing"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/containermgr"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// mountKey is the identity-stripped projection of a MountSpec used to
// compare two specs "modulo identity": container path + ro flag +
// whether the source is a named volume vs a bind. The host *source*
// path is intentionally dropped because per-agent host dirs are stable
// across incarnations and the worktree/git-dir/gitconfig paths are
// keyed on the agent UUID, not the incarnation — they are identical
// spawn-vs-restart for the same agent.
type mountKey struct {
	ContainerPath string
	ReadOnly      bool
	IsVolume      bool
}

func mountKeysOf(mounts []containermgr.MountSpec) []mountKey {
	out := make([]mountKey, 0, len(mounts))
	for _, m := range mounts {
		out = append(out, mountKey{
			ContainerPath: m.ContainerPath,
			ReadOnly:      m.ReadOnly,
			IsVolume:      m.VolumeName != "" && m.HostPath == "",
		})
	}
	return out
}

// fullSpecInput returns a builder input exercising ALL six mounts: a
// worktree workspace, the git-dir, gitconfig, claude-projects, ssh, and
// a claude_seed named volume. The def lists both "worktree" and "ssh"
// mount classes so the projection is faithful.
func fullSpecInput(t *testing.T) agentContainerSpecInput {
	t.Helper()
	agentUUID := uuid.New()
	seedVol := ClaudeSeedVolumeName(agentUUID)
	return agentContainerSpecInput{
		Def: sextantproto.AgentDefinition{
			UUID:     agentUUID,
			Name:     "alpha",
			Template: "default",
			Runtime: sextantproto.RuntimeConfig{
				Model:          "claude-opus-4-7[1m]",
				PermissionCeil: "auto",
				InitialPrompt:  "you are alpha",
			},
			Sandbox: sextantproto.SandboxConfig{
				Image:  "sextant-sidecar:latest",
				Mounts: []string{"worktree", "ssh"},
				Env:    map[string]string{"SEXTANT_DRIVER": "mock"},
			},
		},
		IncarnationID:          uuid.New(),
		JWT:                    "jwt-token",
		HostID:                 "host-1",
		NATSURL:                "nats://localhost:4222",
		NATSUser:               "operator",
		NATSPassword:           "secret",
		MCPURL:                 "http://localhost:5172/mcp",
		Model:                  "claude-opus-4-7[1m]",
		APIKey:                 "sk-ant-test",
		WorkspacePath:          "/wt/feat-default-deadbeef",
		GitDirHostPath:         "/repo/.git",
		GitConfigHostPath:      "/ws/gitconfig-" + agentUUID.String(),
		ClaudeProjectsHostPath: "/data/agents/" + agentUUID.String() + "/claude-projects",
		SSHHostPath:            "/home/op/.ssh",
		ClaudeSeedMount: &containermgr.MountSpec{
			VolumeName:    seedVol,
			ContainerPath: "/home/agent/.claude",
		},
	}
}

// TestBuildAgentContainerSpecAllSixMounts confirms the builder emits all
// six mounts, in the documented order, when every source is present.
// This is the spawn-shape baseline the restart projection must match.
func TestBuildAgentContainerSpecAllSixMounts(t *testing.T) {
	t.Parallel()
	spec := buildAgentContainerSpec(fullSpecInput(t))

	wantTargets := []string{
		WorkspaceMountPath,
		"/repo/.git",
		"/home/agent/.gitconfig",
		"/home/agent/.claude/projects",
		"/home/agent/.ssh",
		"/home/agent/.claude",
	}
	if len(spec.Mounts) != len(wantTargets) {
		t.Fatalf("mount count = %d, want %d; mounts = %+v", len(spec.Mounts), len(wantTargets), spec.Mounts)
	}
	for i, want := range wantTargets {
		if spec.Mounts[i].ContainerPath != want {
			t.Errorf("mount[%d] container path = %q, want %q", i, spec.Mounts[i].ContainerPath, want)
		}
	}
	// gitconfig + ssh are read-only; the rest are rw.
	for _, m := range spec.Mounts {
		switch m.ContainerPath {
		case "/home/agent/.gitconfig", "/home/agent/.ssh":
			if !m.ReadOnly {
				t.Errorf("%s must be ReadOnly", m.ContainerPath)
			}
		default:
			if m.ReadOnly {
				t.Errorf("%s must be rw, got ro", m.ContainerPath)
			}
		}
	}
	if spec.Cmd[0] != "/opt/sextant/sidecar/entrypoint.sh" {
		t.Errorf("Cmd = %v, want sidecar entrypoint", spec.Cmd)
	}
	if spec.Labels[LabelSpecFingerprint] == "" {
		t.Error("spec fingerprint label not stamped")
	}
}

// TestBuildAgentContainerSpecLosslessAcrossIdentity is the unit-level
// expression of the C0 acceptance bar: the spawn build and the restart
// build of the SAME agent definition produce IDENTICAL mount sets and
// IDENTICAL env keys, differing ONLY in identity (incarnation id, JWT).
// This is the drift class the single-source builder eliminates by
// construction — there is no longer a separate restart code path that
// can forget a mount.
func TestBuildAgentContainerSpecLosslessAcrossIdentity(t *testing.T) {
	t.Parallel()

	base := fullSpecInput(t)

	// "spawn" build.
	spawnIn := base
	spawnIn.IncarnationID = uuid.New()
	spawnIn.JWT = "jwt-spawn"
	spawnSpec := buildAgentContainerSpec(spawnIn)

	// "restart" build: same def + same resolved host paths (restart
	// reconstructs them per-UUID, identically), only identity differs.
	restartIn := base
	restartIn.IncarnationID = uuid.New()
	restartIn.JWT = "jwt-restart"
	restartSpec := buildAgentContainerSpec(restartIn)

	// Mount sets are identical modulo identity (the host source paths
	// are UUID-keyed, so they're literally identical here too).
	spawnKeys := mountKeysOf(spawnSpec.Mounts)
	restartKeys := mountKeysOf(restartSpec.Mounts)
	if len(spawnKeys) != len(restartKeys) {
		t.Fatalf("mount count drift: spawn=%d restart=%d", len(spawnKeys), len(restartKeys))
	}
	for i := range spawnKeys {
		if spawnKeys[i] != restartKeys[i] {
			t.Errorf("mount[%d] drift: spawn=%+v restart=%+v", i, spawnKeys[i], restartKeys[i])
		}
	}

	// Env KEY sets are identical (values differ only on identity keys).
	spawnEnvKeys := sortedKeys(spawnSpec.Env)
	restartEnvKeys := sortedKeys(restartSpec.Env)
	if len(spawnEnvKeys) != len(restartEnvKeys) {
		t.Fatalf("env key drift: spawn=%v restart=%v", spawnEnvKeys, restartEnvKeys)
	}
	for i := range spawnEnvKeys {
		if spawnEnvKeys[i] != restartEnvKeys[i] {
			t.Errorf("env key[%d] drift: spawn=%q restart=%q", i, spawnEnvKeys[i], restartEnvKeys[i])
		}
	}

	// The fingerprint is identical: it folds in only the image, mount
	// targets, and env keys — none of which carry incarnation identity.
	// Equal fingerprints across spawn/restart is exactly what P2 drift
	// detection relies on (a restart of an unchanged def must NOT look
	// like drift).
	if spawnSpec.Labels[LabelSpecFingerprint] != restartSpec.Labels[LabelSpecFingerprint] {
		t.Errorf("fingerprint drift across identity: spawn=%q restart=%q",
			spawnSpec.Labels[LabelSpecFingerprint], restartSpec.Labels[LabelSpecFingerprint])
	}
}

// TestSpecFingerprintChangesWithSpecNotIdentity pins the fingerprint's
// contract: it changes when an input the builder controls changes
// (image, a mount, an env key) and does NOT change when only identity
// (incarnation id / JWT / env *values*) changes. This is the seed for
// P2 drift detection (RFC §5.6).
func TestSpecFingerprintChangesWithSpecNotIdentity(t *testing.T) {
	t.Parallel()
	base := fullSpecInput(t)
	baseFP := buildAgentContainerSpec(base).Labels[LabelSpecFingerprint]

	// Identity-only change → same fingerprint.
	idOnly := base
	idOnly.IncarnationID = uuid.New()
	idOnly.JWT = "totally-different-token"
	if got := buildAgentContainerSpec(idOnly).Labels[LabelSpecFingerprint]; got != baseFP {
		t.Errorf("fingerprint changed on identity-only delta: base=%q got=%q", baseFP, got)
	}

	// Image change → different fingerprint.
	img := base
	img.Def.Sandbox.Image = "sextant-sidecar:next"
	if got := buildAgentContainerSpec(img).Labels[LabelSpecFingerprint]; got == baseFP {
		t.Error("fingerprint unchanged when image changed")
	}

	// Dropping a mount → different fingerprint (this is the drift the
	// pre-C0 restart produced: a missing gitconfig/ssh/git-dir).
	noSSH := base
	noSSH.SSHHostPath = ""
	if got := buildAgentContainerSpec(noSSH).Labels[LabelSpecFingerprint]; got == baseFP {
		t.Error("fingerprint unchanged when the ssh mount was dropped")
	}

	// Adding an env key → different fingerprint.
	envKey := base
	envKey.Def.Sandbox.Env = map[string]string{"SEXTANT_DRIVER": "mock", "EXTRA": "1"}
	if got := buildAgentContainerSpec(envKey).Labels[LabelSpecFingerprint]; got == baseFP {
		t.Error("fingerprint unchanged when an env key was added")
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// insertion sort — env maps are small
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
