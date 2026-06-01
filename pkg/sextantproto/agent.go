package sextantproto

import "github.com/google/uuid"

// DesiredState is the operator-written intent half of the spec/status
// split (control-plane RFC §5.2, Appendix C). The operator writes it;
// the reconciler treats it as read-only. It answers "what do I want."
//
// Three values only — the imperative verbs project onto them:
//   - spawn  → run
//   - stop   → paused
//   - archive→ archived
//
// `restart` has no desired-state of its own; it bumps
// AgentSpec.ReactuationNonce instead (k8s `rollout restart`-style).
type DesiredState string

const (
	// DesiredRun means the operator wants a healthy running incarnation.
	DesiredRun DesiredState = "run"
	// DesiredPaused means the operator wants the agent stopped but
	// retained (the old `kill` / stop semantics): no container, record
	// kept, name still held.
	DesiredPaused DesiredState = "paused"
	// DesiredArchived means the operator wants the agent torn down and
	// its name released. Terminal intent.
	DesiredArchived DesiredState = "archived"
)

// IsValid reports whether s is a recognized DesiredState.
func (s DesiredState) IsValid() bool {
	switch s {
	case DesiredRun, DesiredPaused, DesiredArchived:
		return true
	default:
		return false
	}
}

// ObservedState is the reconciler-written observation half of the
// spec/status split (RFC §5.2, Appendix C). The reconciler is the SOLE
// writer; the operator reads it. It answers "what is true."
type ObservedState string

const (
	// ObservedPending means the reconciler has actuated (or is about to
	// actuate) a container that has not yet reached a healthy running
	// state. The transient state between "decided to run" and "running."
	ObservedPending ObservedState = "pending"
	// ObservedRunning means a live container exists for the current
	// incarnation.
	ObservedRunning ObservedState = "running"
	// ObservedCrashed means the sidecar published a `crashed` terminal —
	// an observed failure cause (outranks a daemon-inferred lost).
	ObservedCrashed ObservedState = "crashed"
	// ObservedLost means the daemon inferred the container is gone with
	// no sidecar-observed terminal (OOM, daemon outage, hard kill).
	ObservedLost ObservedState = "lost"
	// ObservedEnded means the sidecar published an `ended` clean
	// terminal — the agent finished its work.
	ObservedEnded ObservedState = "ended"
)

// IsValid reports whether s is a recognized ObservedState.
func (s ObservedState) IsValid() bool {
	switch s {
	case ObservedPending, ObservedRunning, ObservedCrashed, ObservedLost, ObservedEnded:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether the observed state is one a healthy agent
// has left behind — the reconciler does not re-actuate out of a
// terminal observation except via a reactuation nonce bump (restart) or
// (in P1) the recovery branch.
func (s ObservedState) IsTerminal() bool {
	switch s {
	case ObservedCrashed, ObservedLost, ObservedEnded:
		return true
	default:
		return false
	}
}

// RestartPolicy is the per-agent recovery intent (RFC §5.3). P0 records
// and defaults it; the recovery branch that consumes it lands in
// [[feat-ctl-p1-recovery]]. Kept in P0's spec so the schema is the final
// shape and P1 is a pure behavior add, not another record change.
type RestartPolicy string

const (
	// RestartAlways restarts on any exit (clean or failure).
	RestartAlways RestartPolicy = "Always"
	// RestartOnFailure restarts only on a non-zero / killed exit. Default.
	RestartOnFailure RestartPolicy = "OnFailure"
	// RestartNever never auto-restarts.
	RestartNever RestartPolicy = "Never"
)

// IsValid reports whether p is a recognized RestartPolicy.
func (p RestartPolicy) IsValid() bool {
	switch p {
	case RestartAlways, RestartOnFailure, RestartNever:
		return true
	default:
		return false
	}
}

// RuntimeConfig holds the SDK runtime knobs (specs/architecture.md §2).
type RuntimeConfig struct {
	Model          string            `json:"model"`
	Provider       string            `json:"provider,omitempty"`
	Params         map[string]string `json:"params,omitempty"`
	PermissionMode string            `json:"permission_mode,omitempty"`
	SessionID      *string           `json:"session_id,omitempty"`
	InitialPrompt  string            `json:"initial_prompt,omitempty"`
	PermissionCeil string            `json:"permission_ceiling,omitempty"`
}

// ResourceLimits caps the resources a single incarnation may consume.
// Zero values mean "no host-enforced cap" — the container default applies.
type ResourceLimits struct {
	CPUShares int64 `json:"cpu_shares,omitempty"`
	MemoryMiB int64 `json:"memory_mib,omitempty"`
}

// SandboxConfig describes the container boundary for an agent. Mount
// classes resolve to container mounts at spawn time per
// architecture.md §3 and §11b.
type SandboxConfig struct {
	Image        string            `json:"image"`
	Mounts       []string          `json:"mounts,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Credentials  []string          `json:"credentials,omitempty"`
	ResourceLims ResourceLimits    `json:"resource_limits,omitempty"`
}

// AgentSpec is the DESIRED half of the agent record (RFC §5.2,
// Appendix C). Operator-written; the reconciler treats it as read-only.
// "What I want."
type AgentSpec struct {
	// Desired is the operator's intent: run / paused / archived.
	Desired DesiredState `json:"desired"`
	// Runtime + Sandbox + Tools + HostPin are the immutable-ish shape the
	// container is projected from (moved here from the old top-level
	// AgentDefinition fields). buildAgentContainerSpec reads them.
	Runtime RuntimeConfig `json:"runtime"`
	Sandbox SandboxConfig `json:"sandbox"`
	Tools   []string      `json:"tools,omitempty"`
	HostPin *string       `json:"host_pin,omitempty"`
	// RestartPolicy is recorded here in P0 (default OnFailure) and
	// consumed by the P1 recovery branch. RFC §5.3.
	RestartPolicy RestartPolicy `json:"restart_policy,omitempty"`
	// GraceSeconds is the SIGTERM→SIGKILL window the reconciler hands
	// docker stop. Zero falls back to the daemon default (30s, RFC §8).
	GraceSeconds int `json:"grace_seconds,omitempty"`
	// Generation increments on every spec edit (RFC §5.6). The reconciler
	// stamps Status.ObservedGeneration once it has actuated the spec at
	// this generation; generation == observed_generation ⇒ caught up.
	Generation int `json:"generation"`
	// ReactuationNonce is bumped by `restart` to force a fresh incarnation
	// (k8s `rollout restart`-style, RFC §5 lead). The reconciler
	// re-actuates whenever Status.ObservedNonce < ReactuationNonce.
	ReactuationNonce int `json:"reactuation_nonce"`
}

// LastExit records the most recent observed container exit.
type LastExit struct {
	Code   *int      `json:"code,omitempty"`
	Reason string    `json:"reason,omitempty"`
	At     Timestamp `json:"at,omitempty"`
}

// CrashWindow is the windowed restart-budget counter (RFC §8). P0 carries
// the shape; the P1 recovery branch increments it and trips the budget.
type CrashWindow struct {
	Count int       `json:"count,omitempty"`
	Since Timestamp `json:"since,omitempty"`
}

// AgentStatusRecord is the OBSERVED half of the agent record (RFC §5.2,
// Appendix C). The reconciler is the SOLE writer; the operator reads it.
// "What is true." Named ...Record to disambiguate from the AgentStatus
// wire response type returned by get_agent_status (rpcverbs.go).
type AgentStatusRecord struct {
	// Observed is the reconciler's read of reality: pending / running /
	// crashed / lost / ended.
	Observed ObservedState `json:"observed"`
	// Phase is a human-facing rollup string (today mirrors Observed; a
	// slot for richer phrasing later).
	Phase string `json:"phase,omitempty"`
	// CurrentIncarnationID anchors the live (or most-recent) incarnation.
	// Moved here from the old top-level field — it is observed run
	// identity, not desired spec. The incarnation-ID CAS the lifecycle
	// machinery relied on now gates on this field.
	CurrentIncarnationID uuid.UUID `json:"current_incarnation_id,omitempty"`
	// ObservedGeneration is the last AgentSpec.Generation the reconciler
	// has actuated (RFC §5.6). Equal to Generation ⇒ caught up.
	ObservedGeneration int `json:"observed_generation,omitempty"`
	// ObservedNonce is the last AgentSpec.ReactuationNonce the reconciler
	// has actuated. observed_nonce < reactuation_nonce ⇒ a restart is
	// pending.
	ObservedNonce int `json:"observed_nonce,omitempty"`
	// SpecFingerprint is the hash of what was actually built (RFC §5.6).
	// The P2 drift branch compares it against hash(spec).
	SpecFingerprint string `json:"spec_fingerprint,omitempty"`
	// WireEpoch is the running sidecar's epoch (RFC §5.8) — the skew
	// check the P2 drift branch consumes.
	WireEpoch int `json:"wire_epoch,omitempty"`
	// RestartCount is the monotonic lifetime restart counter (operator
	// visibility, RFC §8).
	RestartCount int `json:"restart_count,omitempty"`
	// CrashWindow is the windowed crash budget (RFC §8). The P1 recovery
	// branch increments it on every auto-restart and flips the agent to
	// terminal `crashed` once it exceeds the budget (5 / 10 min).
	CrashWindow CrashWindow `json:"crash_window,omitempty"`
	// BackoffUntil is the wall-clock instant before which the recovery
	// branch must NOT re-actuate (RFC §8: exponential backoff 10s ×2 cap
	// 300s). The reconciler stamps it after each auto-restart; a pass that
	// finds observed∈{lost,crashed} before this deadline holds off. Zero
	// means "no backoff pending."
	BackoffUntil Timestamp `json:"backoff_until,omitempty"`
	// RunningSince is the wall-clock the current incarnation was first
	// observed healthy-running. The recovery branch uses it for the
	// stable-run backoff reset (RFC §8: reset only after ≥10 min of
	// continuous run, an INDEPENDENT constant). Zero until first observed
	// running.
	RunningSince Timestamp `json:"running_since,omitempty"`
	// LivenessFailures counts CONSECUTIVE health-check failures for the
	// current incarnation (RFC §8: 3 / 10s → restart path). Reset to 0 on
	// any healthy observation. Catches a wedged-but-running agent docker
	// `die` never fires for.
	LivenessFailures int `json:"liveness_failures,omitempty"`
	// LastExit records the most recent observed container exit.
	LastExit *LastExit `json:"last_exit,omitempty"`
	// SessionSnapshot is the durable JSONL snapshot-on-stop path (RFC
	// §5.10). Populated by the S0 / reconciler snapshot work.
	SessionSnapshot string `json:"session_snapshot,omitempty"`
	// LastHeartbeatAt is the last observed heartbeat timestamp.
	LastHeartbeatAt Timestamp `json:"last_heartbeat_at,omitempty"`
	// LastReconciledAt is the wall-clock of the last reconcile pass that
	// touched this record.
	LastReconciledAt Timestamp `json:"last_reconciled_at,omitempty"`
}

// AgentDefinition is the durable declarative record describing an agent
// (control-plane RFC §5.2, Appendix C). Stored in NATS KV
// (`agent_definitions.<uuid>`).
//
// The record is split into operator-written **Spec** (desired intent)
// and reconciler-written **Status** (observed truth). The reconciler is
// the sole writer of Status and the sole actuator of the container
// runtime; handlers only edit Spec. See pkg/sextantd/reconcile.go.
//
// Per architecture.md §2 the UUID is permanent and the Name is unique
// among non-archived agents.
type AgentDefinition struct {
	// --- metadata (identity; set at create, ~immutable) ---
	UUID     uuid.UUID `json:"uuid"`
	Name     string    `json:"name"`
	Type     string    `json:"type"`
	Template string    `json:"template,omitempty"`

	// --- spec: DESIRED state (operator-written) ---
	Spec AgentSpec `json:"spec"`

	// --- status: OBSERVED state (reconciler-written, SOLE writer) ---
	Status AgentStatusRecord `json:"status"`

	// Version is the KV CAS / audit version, bumped on every write
	// (distinct from Spec.Generation, which is the spec-edit counter).
	Version     uint64    `json:"version"`
	CreatedAt   Timestamp `json:"created_at"`
	UpdatedAt   Timestamp `json:"updated_at"`
	EscalateTo  *string   `json:"escalate_to,omitempty"`
	Description string    `json:"description,omitempty"`
}

// Lifecycle projects the spec/status split back into a single
// operator-facing rollup string — the answer `list_agents`,
// `get_agent_status`, and the TUIs render. It is a DERIVED projection,
// not a stored field: callers read it, no one writes it.
//
// The rule (RFC §5.2: "for every agent where desired=run but
// observed≠running, act"):
//   - desired=archived          → archived  (terminal intent wins)
//   - desired=paused            → paused
//   - desired=run, observed set → the observed value (running / crashed
//     / lost / ended / pending → "defined" when pending pre-actuation)
//   - otherwise                 → defined
//
// The returned LifecycleState reuses the legacy wire strings so the
// read surface (CLI filters, TUIs) needs no value remapping.
func (d AgentDefinition) Lifecycle() LifecycleState {
	switch d.Spec.Desired {
	case DesiredArchived:
		return LifecycleArchived
	case DesiredPaused:
		return LifecyclePaused
	}
	// desired == run (or unset, treated as run for legacy records).
	switch d.Status.Observed {
	case ObservedRunning:
		return LifecycleRunning
	case ObservedCrashed:
		return LifecycleCrashedState
	case ObservedLost:
		return LifecycleLostState
	case ObservedEnded:
		return LifecycleEndedState
	case ObservedPending:
		// Actuation in flight; surface as "defined" (not yet running) so
		// the operator sees a stable pre-running label.
		return LifecycleDefined
	default:
		return LifecycleDefined
	}
}

// LifecycleState enumerates the legacy single-field lifecycle strings.
// Retained as the wire/render vocabulary the read surface still speaks;
// AgentDefinition.Lifecycle() projects the spec/status split onto these
// values. The durable record no longer stores a LifecycleState directly.
type LifecycleState string

const (
	LifecycleDefined  LifecycleState = "defined"
	LifecycleRunning  LifecycleState = "running"
	LifecyclePaused   LifecycleState = "paused"
	LifecycleArchived LifecycleState = "archived"
	// LifecycleEndedState is the terminal "sidecar exited cleanly" state.
	// Suffixed "...State" because LifecycleEnded names a LifecycleEvent in
	// payloads.go; both share the wire string "ended".
	LifecycleEndedState LifecycleState = "ended"
	// LifecycleCrashedState is the terminal "sidecar exited with failure"
	// state.
	LifecycleCrashedState LifecycleState = "crashed"
	// LifecycleLostState is the daemon-inferred terminal state set when the
	// container is absent and no sidecar lifecycle was observed.
	LifecycleLostState LifecycleState = "lost"
)

// IsValid reports whether s is a recognized LifecycleState.
func (s LifecycleState) IsValid() bool {
	switch s {
	case LifecycleDefined, LifecycleRunning, LifecyclePaused, LifecycleArchived,
		LifecycleEndedState, LifecycleCrashedState, LifecycleLostState:
		return true
	default:
		return false
	}
}

// AgentIncarnation tracks one live process for an agent. Multiple
// incarnations over time share the AgentDefinition.UUID; each
// incarnation has its own ID, container, and JWT.
//
// Per architecture.md §2 an agent has at most one running incarnation
// at a time.
type AgentIncarnation struct {
	IncarnationID uuid.UUID        `json:"incarnation_id"`
	AgentUUID     uuid.UUID        `json:"agent_uuid"`
	StartedAt     Timestamp        `json:"started_at"`
	EndedAt       *Timestamp       `json:"ended_at,omitempty"`
	HostID        string           `json:"host_id"`
	ContainerID   string           `json:"container_id,omitempty"`
	State         IncarnationState `json:"state"`
	ExitCode      *int             `json:"exit_code,omitempty"`
	JWTKeyID      string           `json:"jwt_key_id,omitempty"`
}

// IncarnationState enumerates the legal values for AgentIncarnation.State.
type IncarnationState string

const (
	IncarnationStarting IncarnationState = "starting"
	IncarnationReady    IncarnationState = "ready"
	IncarnationExited   IncarnationState = "exited"
	IncarnationFailed   IncarnationState = "failed"
)

// IsValid reports whether s is a recognized IncarnationState.
func (s IncarnationState) IsValid() bool {
	switch s {
	case IncarnationStarting, IncarnationReady, IncarnationExited, IncarnationFailed:
		return true
	default:
		return false
	}
}
