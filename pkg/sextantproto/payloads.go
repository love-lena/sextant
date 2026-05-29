package sextantproto

import "github.com/google/uuid"

// AgentFramePayload carries one observable unit from the Claude SDK loop:
// assistant text, a tool call, a tool result, or a system note. The full
// SDK output is captured verbatim under Body for downstream tooling that
// wants to reconstruct conversations.
type AgentFramePayload struct {
	FrameKind FrameKind         `json:"frame_kind"`
	SessionID string            `json:"session_id,omitempty"`
	ToolName  string            `json:"tool_name,omitempty"`
	Body      map[string]any    `json:"body"`
	Tokens    *FrameTokens      `json:"tokens,omitempty"`
	Tags      map[string]string `json:"tags,omitempty"`
}

// FrameKind enumerates AgentFramePayload.FrameKind.
type FrameKind string

const (
	FrameAssistantText FrameKind = "assistant_text"
	FrameToolCall      FrameKind = "tool_call"
	FrameToolResult    FrameKind = "tool_result"
	FrameSystemNote    FrameKind = "system_note"
	FrameError         FrameKind = "error"
)

// AllFrameKinds returns every AgentFramePayload.FrameKind in canonical
// order. Used by code generation (the wire.json manifest + generated TS
// constants) and tests; do not rely on it for hot paths.
func AllFrameKinds() []FrameKind {
	return []FrameKind{
		FrameAssistantText,
		FrameToolCall,
		FrameToolResult,
		FrameSystemNote,
		FrameError,
	}
}

// FrameTokens carries optional token-accounting numbers reported by the SDK.
type FrameTokens struct {
	Input        int64 `json:"input"`
	Output       int64 `json:"output"`
	CacheRead    int64 `json:"cache_read,omitempty"`
	CacheCreated int64 `json:"cache_created,omitempty"`
}

// LifecyclePayload describes an agent lifecycle transition.
type LifecyclePayload struct {
	IncarnationID uuid.UUID        `json:"incarnation_id"`
	AgentUUID     uuid.UUID        `json:"agent_uuid"`
	Transition    LifecycleEvent   `json:"transition"`
	State         IncarnationState `json:"state"`
	Reason        string           `json:"reason,omitempty"`
	ExitCode      *int             `json:"exit_code,omitempty"`
	// Source records what produced this lifecycle envelope. Empty
	// (= sidecar) for back-compat with old payloads.
	Source LifecycleSource `json:"source,omitempty"`
}

// LifecycleSource identifies the producer of a lifecycle envelope.
type LifecycleSource string

const (
	// LifecycleSourceSidecar is the default (empty string) for back-compat
	// with old payloads produced by sidecars before this field existed.
	LifecycleSourceSidecar          LifecycleSource = ""
	LifecycleSourceReconciler       LifecycleSource = "reconciler"
	LifecycleSourceContainerWatcher LifecycleSource = "container_watcher"
)

// LifecycleEvent enumerates the kinds of transitions a lifecycle envelope
// can record.
type LifecycleEvent string

const (
	LifecycleStarted        LifecycleEvent = "started"
	LifecycleEnded          LifecycleEvent = "ended"
	LifecyclePausedEvent    LifecycleEvent = "paused"
	LifecycleResumedEvent   LifecycleEvent = "resumed"
	LifecycleArchivedEvent  LifecycleEvent = "archived"
	LifecycleRestartedEvent LifecycleEvent = "restarted"
	LifecycleCrashedEvent   LifecycleEvent = "crashed"
	// LifecycleTurnEnded is published by the sidecar's SDK driver loop
	// when one turn (one prompt → one assistant response, including any
	// tool-use round-trips) completes. Reason="error" distinguishes a
	// failed turn from a clean one. See
	// specs/components/sidecar-image.md §"Sidecar entrypoint".
	LifecycleTurnEnded LifecycleEvent = "turn_ended"
	// LifecycleLostEvent is published by the daemon (reconciler or
	// container watcher) when a container is absent and no sidecar
	// lifecycle was observed. Distinct from LifecycleCrashedEvent which
	// requires the sidecar itself to publish.
	LifecycleLostEvent LifecycleEvent = "lost"
)

// AuditPayload records an auth-relevant action. Mirrors the ClickHouse
// `audit` table columns one-to-one for shipper simplicity.
type AuditPayload struct {
	Actor              string         `json:"actor"`
	AgentUUID          *uuid.UUID     `json:"agent_uuid,omitempty"`
	Action             string         `json:"action"`
	CapabilityRequired string         `json:"capability_required,omitempty"`
	Result             AuditResult    `json:"result"`
	Details            map[string]any `json:"details,omitempty"`
}

// AuditResult enumerates the outcome of an audited action.
type AuditResult string

const (
	AuditAllowed AuditResult = "allowed"
	AuditDenied  AuditResult = "denied"
	AuditError   AuditResult = "error"
)

// UserInputRequestPayload — an agent asking for human input.
type UserInputRequestPayload struct {
	RequestID uuid.UUID         `json:"request_id"`
	FromUUID  uuid.UUID         `json:"from_uuid"`
	Question  string            `json:"question"`
	Options   []string          `json:"options,omitempty"`
	Urgency   string            `json:"urgency,omitempty"`
	GroupWith *uuid.UUID        `json:"group_with,omitempty"`
	Context   map[string]string `json:"context,omitempty"`
}

// UserInputResponsePayload — an answer or escalation to a request.
type UserInputResponsePayload struct {
	RequestID  uuid.UUID  `json:"request_id"`
	Decision   InputReply `json:"decision"`
	Answer     string     `json:"answer,omitempty"`
	EscalateTo *string    `json:"escalate_to,omitempty"`
}

// InputReply enumerates the kinds of replies a reviewer can make.
type InputReply string

const (
	InputAnswer   InputReply = "answer"
	InputEscalate InputReply = "escalate"
	InputDefer    InputReply = "defer"
	InputBatch    InputReply = "batch"
)

// HeartbeatPayload — periodic health beat from a running sidecar.
type HeartbeatPayload struct {
	AgentUUID      uuid.UUID         `json:"agent_uuid"`
	IncarnationID  uuid.UUID         `json:"incarnation_id"`
	HostID         string            `json:"host_id"`
	UptimeSeconds  int64             `json:"uptime_seconds"`
	LastFrameTs    *Timestamp        `json:"last_frame_ts,omitempty"`
	PendingPrompts int               `json:"pending_prompts,omitempty"`
	ResourceUsage  map[string]string `json:"resource_usage,omitempty"`
}
