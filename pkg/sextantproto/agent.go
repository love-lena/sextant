package sextantproto

import "github.com/google/uuid"

// LifecycleState enumerates the lifecycle states an agent definition can
// hold (specs/architecture.md §2). Stored in NATS KV under
// `agent_definitions.<uuid>` and replicated to ClickHouse via the shipper.
type LifecycleState string

const (
	LifecycleDefined  LifecycleState = "defined"
	LifecycleRunning  LifecycleState = "running"
	LifecyclePaused   LifecycleState = "paused"
	LifecycleArchived LifecycleState = "archived"
	// LifecycleEndedState is the terminal "sidecar exited cleanly"
	// state. The daemon's lifecycle watcher writes this when the
	// sidecar publishes `transition=ended` on agents.<uuid>.lifecycle.
	// Suffixed "...State" because LifecycleEnded names a LifecycleEvent
	// in payloads.go; both share the wire string "ended".
	LifecycleEndedState LifecycleState = "ended"
	// LifecycleCrashedState is the terminal "sidecar exited with failure"
	// state — set when the sidecar publishes `transition=crashed`.
	LifecycleCrashedState LifecycleState = "crashed"
)

// IsValid reports whether s is a recognized LifecycleState.
func (s LifecycleState) IsValid() bool {
	switch s {
	case LifecycleDefined, LifecycleRunning, LifecyclePaused, LifecycleArchived,
		LifecycleEndedState, LifecycleCrashedState:
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

// AgentDefinition is the durable record describing an agent. Stored in
// NATS KV (`agent_definitions.<uuid>`). Per architecture.md §2 the UUID
// is permanent and the Name is unique among non-archived agents.
//
// Tools enumerates the capability allowlist; the JWT issued at spawn
// time carries this list verbatim.
type AgentDefinition struct {
	UUID      uuid.UUID      `json:"uuid"`
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	Template  string         `json:"template,omitempty"`
	Runtime   RuntimeConfig  `json:"runtime"`
	Sandbox   SandboxConfig  `json:"sandbox"`
	Tools     []string       `json:"tools,omitempty"`
	HostPin   *string        `json:"host_pin,omitempty"`
	Lifecycle LifecycleState `json:"lifecycle"`
	// CurrentIncarnationID is the UUID of the live (or most-recent)
	// AgentIncarnation. Set by spawn_agent + restart_agent before the
	// new incarnation publishes its `started` lifecycle envelope, so
	// the daemon's lifecycle watcher has an authoritative anchor for
	// stale-incarnation filtering — without it, a delayed `ended`
	// envelope from the prior incarnation can clobber the freshly-
	// started record during the restart handoff window.
	//
	// Zero value (uuid.Nil) is acceptable in two cases:
	//   - pre-incarnation-field installs (older agents written before
	//     this field was added). The watcher allows envelopes through
	//     when the field is Nil (warm-up).
	//   - terminal states (kill/archive). Cleared by those handlers
	//     since there's no live incarnation.
	//
	// Schema evolution: omitempty so older JSON consumers parse fine.
	CurrentIncarnationID uuid.UUID `json:"current_incarnation_id,omitempty"`
	Version              uint64    `json:"version"`
	CreatedAt            Timestamp `json:"created_at"`
	UpdatedAt            Timestamp `json:"updated_at"`
	EscalateTo           *string   `json:"escalate_to,omitempty"`
	Description          string    `json:"description,omitempty"`
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
