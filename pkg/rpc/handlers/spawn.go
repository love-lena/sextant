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
// actuator. Spec calls for 24h.
const SpawnJWTLifetime = 24 * time.Hour

// LabelAgentUUID etc. are the container labels every actuation stamps.
// Tests rely on these stable strings for cleanup.
const (
	LabelAgentUUID      = "sextant.agent_uuid"
	LabelAgentName      = "sextant.agent_name"
	LabelHostID         = "sextant.host_id"
	LabelIncarnationID  = "sextant.incarnation_id"
	LabelTemplate       = "sextant.template"
	LabelTestRun        = "sextant.test_run"
	WorkspaceMountPath  = "/workspace"
	defaultGraceSeconds = 30
)

// AgentMutableKV is the read+write surface the spawn handler needs on
// the agent_definitions and agent_incarnations buckets. Narrowed so
// tests can pass a fake without bringing JetStream up.
type AgentMutableKV interface {
	AgentKV
	Put(ctx context.Context, key string, value []byte) (uint64, error)
	// Update is the CAS write: writes value when the entry's last
	// revision matches `revision`. Real jetstream.KeyValue returns
	// jetstream.ErrKeyExists on revision mismatch; handlers treat that as
	// "concurrent writer slipped in" and re-read + re-apply before
	// retrying.
	Update(ctx context.Context, key string, value []byte, revision uint64) (uint64, error)
	Delete(ctx context.Context, key string, opts ...jetstream.KVDeleteOpt) error
}

// HistoryWriter records definition mutations to ClickHouse so the
// audit trail survives a NATS data-dir wipe.
type HistoryWriter interface {
	Exec(ctx context.Context, query string, args ...any) error
}

// ReconcileEnqueuer is the hint sink a handler calls after writing
// desired state: it asks the reconcile loop to converge the agent soon.
// The reconciler is level-triggered, so a dropped enqueue only delays
// convergence to the next periodic sweep — it can never desync us
// (RFC §3.2). Nil is acceptable (unit tests without a loop wired).
type ReconcileEnqueuer interface {
	Enqueue(agentID uuid.UUID)
}

// SpawnDeps bundles the dependencies the spawn handler needs. Under the
// declarative model the spawn handler is a thin desired-state writer: it
// validates, builds the record (desired=run), persists it, and enqueues
// a reconcile. It NEVER touches the container runtime — the reconciler
// (the sole actuator) creates the container.
//
// The container-runtime and host-materialization fields (Containers,
// Volumes, Worktree, RepoRoot, NATS*, MCPURL, …) are retained on the
// struct so the daemon can build a single dep bag and hand the
// runtime-bearing subset to the Actuator. The spawn handler itself reads
// only the KV + template + history + enqueue surfaces.
type SpawnDeps struct {
	Definitions    AgentMutableKV
	Incarnations   AgentMutableKV
	Templates      templates.KV
	Containers     ContainerRunner
	CA             *authjwt.CA
	History        HistoryWriter
	WorkspaceRoot  string
	AgentsDataRoot string
	Worktree       WorktreeProvider
	RepoRoot       string
	Volumes        VolumeManager
	HostID         string
	NATSURL        string
	NATSUser       string
	NATSPassword   string
	MCPURL         string
	Issuer         string
	TestRunLabel   string
	// Enqueue, when non-nil, asks the reconcile loop to converge the new
	// agent right after the record lands. Nil falls back to the periodic
	// sweep (still correct, just slower).
	Enqueue ReconcileEnqueuer
	// Now is injected for deterministic timestamps in tests.
	Now func() time.Time
}

// WorktreeProvider is the narrow surface the spawn path + actuator need
// on pkg/worktree.
type WorktreeProvider interface {
	Create(ctx context.Context, name, baseBranch string, owningAgent uuid.UUID) (sextantproto.WorktreeInfo, error)
	Destroy(ctx context.Context, name string, force bool) error
	Resolve(ctx context.Context, name string) (path string, ok bool, err error)
}

// ContainerRunner is the subset of containermgr.Manager the actuator
// calls. Handlers no longer call it directly (RFC §5: sole actuator);
// it lives here because the actuator + spec builder share the package.
type ContainerRunner interface {
	Run(ctx context.Context, spec containermgr.ContainerSpec) (*containermgr.Container, error)
	Stop(ctx context.Context, id string, grace time.Duration) error
}

// ContainerFileCopier is the narrow copy-from-container surface the
// snapshot-on-stop path uses (S0, RFC §5.10). It is satisfied by
// *containermgr.Manager and crucially works on an EXITED container: when
// the reconciler observes an agent leave running it copies the
// authoritative session JSONL out of the (already-stopped) container into
// the agent data dir. Kept separate from ContainerRunner so it is
// optional (nil disables snapshotting) and the existing run/stop fakes
// don't have to grow.
type ContainerFileCopier interface {
	CopyFileFromContainer(ctx context.Context, id, srcPath string) ([]byte, error)
}

// VolumeManager is the subset of containermgr.Manager the actuator uses
// to manage per-agent named volumes (the claude_seed copy-on-spawn
// volume).
type VolumeManager interface {
	EnsureVolume(ctx context.Context, name string, labels map[string]string) (created bool, err error)
	PopulateVolumeFromHostDir(ctx context.Context, volumeName, hostSrc, image string, cmd []string) error
	RemoveVolume(ctx context.Context, name string, force bool) error
}

// NewSpawnAgent returns a Handler implementing `spawn_agent` as a
// desired-state edit (RFC §5: imperative verbs become intent edits).
// Flow:
//
//  1. Decode + validate args.
//  2. Reject duplicate names (unique among non-archived definitions).
//  3. Resolve the template from KV.
//  4. Build a fresh AgentDefinition with spec.desired=run, generation=1,
//     status.observed=pending, observed_generation=0 — the reconciler
//     sees the generation gap and actuates the first incarnation.
//  5. Persist; append the history row; enqueue a reconcile.
//  6. Reply with the new agent UUID.
//
// It does NOT create a container, mount anything, or issue a JWT — the
// reconciler does all of that when it actuates the pending record.
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

		// 4. Build the declarative record. desired=run + generation=1 with
		// observed_generation=0 is the initial-actuation trigger the
		// reconciler converges (decideRun branch 1).
		def := sextantproto.AgentDefinition{
			UUID:        agentUUID,
			Name:        args.Name,
			Type:        "assistant",
			Template:    tpl.Name,
			Description: tpl.Description,
			Spec: sextantproto.AgentSpec{
				Desired: sextantproto.DesiredRun,
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
				Tools:         append([]string(nil), tpl.Permissions...),
				RestartPolicy: sextantproto.RestartOnFailure,
				Generation:    1,
			},
			Status: sextantproto.AgentStatusRecord{
				Observed: sextantproto.ObservedPending,
				Phase:    string(sextantproto.ObservedPending),
			},
			Version:   1,
			CreatedAt: sextantproto.AtTimestamp(now),
			UpdatedAt: sextantproto.AtTimestamp(now),
		}
		if args.HostPin != "" {
			pin := args.HostPin
			def.Spec.HostPin = &pin
		}

		if err := putJSON(ctx, deps.Definitions, agentUUID.String(), def); err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("persist agent definition: %v", err))
		}

		// 5. Append the initial history row (best-effort).
		if deps.History != nil {
			if err := insertDefinitionHistory(ctx, deps.History, def, "spawn"); err != nil {
				fmt.Fprintf(os.Stderr, "spawn_agent: history insert failed for %s: %v\n", agentUUID, err)
			}
		}

		// Hint the reconciler to converge the new pending record now.
		if deps.Enqueue != nil {
			deps.Enqueue.Enqueue(agentUUID)
		}

		return emitOK(emit, sextantproto.SpawnAgentResponse{AgentID: agentUUID})
	}
}

// agentNameInUse returns true if there's an entry in the definitions
// bucket whose Name matches and whose name has NOT been released. A name
// is released only once the archive is FULLY reclaimed (desired=archived
// AND observed=archived) — bug-ctl-archive-volume-leak: an archive that is
// still mid-flight (observed=archiving, e.g. the per-agent volume reclaim
// keeps failing) holds the name so a re-spawn cannot collide with a record
// whose external state isn't reclaimed yet. See AgentDefinition.NameReleased.
//
// O(N) scan over the bucket; very few entries so this is fine.
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
			fmt.Fprintf(os.Stderr, "spawn_agent: agentNameInUse: decode %s: %v\n", key, err)
			continue
		}
		if def.Name == name && !def.NameReleased() {
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

// ensureWorkspaceDir creates the stop-gap workspace dir under root.
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

// writeAgentGitConfig stages a per-agent gitconfig file under root and
// returns its path + a cleanup closure. Content matches
// plans/issues/feat-container-git-config.md.
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

// mountClassListed reports whether class is present in the mount-class
// list. Operates on a raw []string so both the template's Mounts and the
// def's Spec.Sandbox.Mounts (cloned from the template at spawn) answer
// the same question.
func mountClassListed(mounts []string, class string) bool {
	for _, m := range mounts {
		if m == class {
			return true
		}
	}
	return false
}

func containerName(agentName string, incID uuid.UUID) string {
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

// permissionCeilingToSDKMode maps a sextant-internal permission_ceiling
// value to the Claude Agent SDK permissionMode string.
//
//	"auto" or ""  → "acceptEdits"
//	"plan"        → "plan"
func permissionCeilingToSDKMode(ceiling string) string {
	switch ceiling {
	case "plan":
		return "plan"
	default:
		return "acceptEdits"
	}
}

// ClaudeSeedVolumePrefix is the prefix for per-agent Docker named
// volumes that back the claude_seed copy-on-spawn flow.
const ClaudeSeedVolumePrefix = "sextant-claude-seed-"

// ClaudeSeedVolumeName returns the canonical name of the per-agent
// claude_seed volume.
func ClaudeSeedVolumeName(agentUUID uuid.UUID) string {
	return ClaudeSeedVolumePrefix + agentUUID.String()
}

// buildClaudeSeedMount returns the mount appended to the container spec
// for a template with claude_seed set. See the original spawn doc for
// the copy-on-spawn vs readonly-bind modes.
func buildClaudeSeedMount(ctx context.Context, deps SpawnDeps, mode, seedPath string, agentUUID uuid.UUID, image string) (containermgr.MountSpec, func(), error) {
	switch mode {
	case templates.ClaudeSeedModeReadonly:
		return containermgr.MountSpec{
			HostPath:      seedPath,
			ContainerPath: "/home/agent/.claude",
			ReadOnly:      true,
		}, nil, nil
	case templates.ClaudeSeedModeCopyOnSpawn, "":
		if deps.Volumes == nil {
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
			if err := deps.Volumes.PopulateVolumeFromHostDir(ctx, volName, seedPath, image, nil); err != nil {
				_ = deps.Volumes.RemoveVolume(context.Background(), volName, true) //nolint:contextcheck // rollback uses a fresh ctx
				return containermgr.MountSpec{}, nil, fmt.Errorf("populate volume %s from %s: %w", volName, seedPath, err)
			}
		}
		var cleanup func()
		if created {
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

// insertDefinitionHistory writes one row into the
// agent_definitions_history ClickHouse table. The definition column is
// the full AgentDefinition JSON so the history is self-describing.
func insertDefinitionHistory(ctx context.Context, hw HistoryWriter, def sextantproto.AgentDefinition, changeKind string) error {
	raw, err := json.Marshal(def)
	if err != nil {
		return fmt.Errorf("marshal definition: %w", err)
	}
	q := `INSERT INTO agent_definitions_history
		(agent_uuid, version, ts, actor, change_kind, definition)
		VALUES (?, ?, ?, ?, ?, ?)`
	return hw.Exec(
		ctx, q,
		def.UUID.String(),
		def.Version,
		def.UpdatedAt.Time,
		"operator",
		changeKind,
		string(raw),
	)
}
