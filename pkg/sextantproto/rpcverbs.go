package sextantproto

import (
	"time"

	"github.com/google/uuid"
)

// This file holds the verb-specific request/response payload structs that
// appear on the wire as the `payload` field of `rpc_request` and
// `rpc_response` envelopes. They live in sextantproto (not pkg/rpc) so
// JSON Schemas can be regenerated for them and consumed by the TypeScript
// client. See specs/components/client-libraries.md §"Verb payload structs
// live in pkg/sextantproto/".
//
// Verb name constants and CapFor stay in pkg/rpc — they are server-side
// dispatch metadata, not wire payload shapes.

// ListAgentsRequest mirrors the catalog shape for list_agents.
//
// Filter is optional; nil filter matches every agent.
type ListAgentsRequest struct {
	Filter *ListAgentsFilter `json:"filter,omitempty"`
}

// ListAgentsFilter narrows the result set by lifecycle.
//
// In M7 only Lifecycle is supported; additional columns can be added
// without a breaking change (omitempty everywhere).
type ListAgentsFilter struct {
	Lifecycle string `json:"lifecycle,omitempty"`
}

// ListAgentsResponse is the list_agents reply payload.
type ListAgentsResponse struct {
	Agents []AgentSummary `json:"agents"`
}

// AgentSummary projects an AgentDefinition into the list_agents shape.
// Fields chosen to match the spec's AgentSummary row.
type AgentSummary struct {
	UUID      uuid.UUID `json:"uuid"`
	Name      string    `json:"name"`
	Type      string    `json:"type,omitempty"`
	Template  string    `json:"template,omitempty"`
	Lifecycle string    `json:"lifecycle"`
	Version   uint64    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	// Restarts is the monotonic lifetime auto-restart count
	// (Status.RestartCount) — the `RESTARTS` column the operator reads to
	// spot a crash-looper (RFC §2, §8 P1 recovery).
	Restarts int `json:"restarts,omitempty"`
}

// GetAgentStatusRequest is the get_agent_status request payload.
//
// IncludeHeartbeat asks the daemon to attach the latest heartbeat
// observation from the in-memory HeartbeatCache (L1 freshness signal).
// Defaults to false so existing callers keep their narrow response
// shape; `sextant agents check` opts in to drive the defense-in-depth
// `degraded` verdict per
// `plans/issues/feat-agents-check-heartbeat-secondary-signal.md`.
type GetAgentStatusRequest struct {
	AgentID          uuid.UUID `json:"agent_id"`
	IncludeHeartbeat bool      `json:"include_heartbeat,omitempty"`
}

// GetAgentStatusResponse is the get_agent_status reply payload.
type GetAgentStatusResponse struct {
	Status AgentStatus `json:"status"`
}

// AgentStatus is the per-agent snapshot returned by get_agent_status.
//
// Heartbeat is populated only when the request set IncludeHeartbeat=true.
// Nil means either the caller did not ask, or the cache has no entry
// for this agent (e.g., never seen a heartbeat, or the daemon's cache
// is not wired).
//
// SessionLog is populated when the daemon has per-agent session paths
// wired (the spawn handler bind-mounts ~/.claude/projects/ per agent
// per the `feat-agents-context-view` plan). Nil for older agents
// spawned before the bind mount landed, and for daemons that haven't
// configured the agents data root.
type AgentStatus struct {
	UUID      uuid.UUID `json:"uuid"`
	Name      string    `json:"name"`
	Lifecycle string    `json:"lifecycle"`
	Version   uint64    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	// Restarts is the monotonic lifetime auto-restart count
	// (Status.RestartCount); LastExitReason is the most recent observed
	// exit cause (Status.LastExit.Reason). Both surface the P1 recovery
	// score the daemon now keeps (RFC §2).
	Restarts       int                `json:"restarts,omitempty"`
	LastExitReason string             `json:"last_exit_reason,omitempty"`
	Heartbeat      *HeartbeatSnapshot `json:"heartbeat,omitempty"`
	SessionLog     *SessionLogInfo    `json:"session_log,omitempty"`
}

// SessionLogInfo describes how to reach the agent's Claude Code SDK
// session JSONL on the host filesystem. The daemon bind-mounts a
// per-agent host directory at /home/agent/.claude/projects/ inside
// the container, so the SDK's session writer ends up writing to a
// path the host can read directly.
//
//   - ProjectsDir is the host dir corresponding to the container's
//     /home/agent/.claude/projects/. The SDK writes session JSONL
//     files under <ProjectsDir>/<encoded-cwd>/<sessionId>.jsonl.
//   - SessionID is the latest SDK-issued session id persisted to the
//     agent definition by the sidecar. Empty until the agent has
//     completed its first turn.
//
// The CLI `agents context` verb resolves the actual JSONL path by
// walking ProjectsDir for a file matching <SessionID>.jsonl — the
// `<encoded-cwd>` segment is the SDK's URL-encoded representation of
// the in-container cwd, which the daemon doesn't need to mirror.
type SessionLogInfo struct {
	ProjectsDir string `json:"projects_dir,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
}

// HeartbeatSnapshot is the freshness shape attached to AgentStatus when
// the caller opts in via GetAgentStatusRequest.IncludeHeartbeat.
//
//   - LastSeen / AgeSeconds: set when the daemon's HeartbeatCache has
//     observed at least one heartbeat for this agent.
//   - Source: "cache" when the snapshot came from the in-memory cache,
//     "none" when the cache had no entry. Forward-compat for a future
//     KV-backed source ("kv") without changing the wire shape.
type HeartbeatSnapshot struct {
	LastSeen   *time.Time `json:"last_seen,omitempty"`
	AgeSeconds *float64   `json:"age_seconds,omitempty"`
	Source     string     `json:"source"`
}

// ReadFileRequest is the read_file request payload. M7 handler is a stub
// — the shape is defined for forward-compat.
type ReadFileRequest struct {
	AgentID uuid.UUID `json:"agent_id"`
	Path    string    `json:"path"`
}

// ReadFileResponse is the read_file reply payload. M7 never returns
// this — the handler always errors with ErrCodeNotImplemented until
// M11.
type ReadFileResponse struct {
	Content     []byte `json:"content"`
	ContentType string `json:"content_type"`
}

// QueryHistoryRequest is the query_history request payload.
type QueryHistoryRequest struct {
	Filter    QueryHistoryFilter `json:"filter"`
	TimeRange TimeRange          `json:"time_range"`
	Limit     int                `json:"limit,omitempty"`
}

// QueryHistoryFilter narrows the ClickHouse events query. Empty strings
// and zero UUIDs are treated as "any". M7 is exact-match; wildcards land
// when a real consumer needs them.
type QueryHistoryFilter struct {
	Subject   string    `json:"subject,omitempty"`
	FromID    string    `json:"from_id,omitempty"`
	AgentUUID uuid.UUID `json:"agent_uuid,omitempty"`
	Kind      string    `json:"kind,omitempty"`
}

// TimeRange bounds query_history. Zero values are unbounded.
type TimeRange struct {
	Since time.Time `json:"since,omitempty"`
	Until time.Time `json:"until,omitempty"`
}

// QueryHistoryResponse is the query_history reply payload.
//
// Events is always non-nil. The caller treats nil and empty
// interchangeably, but the wire format always emits the field.
type QueryHistoryResponse struct {
	Events []Envelope `json:"events"`
}

// SpawnAgentRequest is the spawn_agent request payload. See
// specs/protocols/rpc-catalog.md §"Agent lifecycle" for the canonical
// shape; DefinitionOverrides is reserved for post-M11 work and ignored
// today.
type SpawnAgentRequest struct {
	Name                string         `json:"name"`
	Template            string         `json:"template"`
	HostPin             string         `json:"host_pin,omitempty"`
	DefinitionOverrides map[string]any `json:"definition_overrides,omitempty"`
}

// SpawnAgentResponse is the spawn_agent reply payload.
type SpawnAgentResponse struct {
	AgentID uuid.UUID `json:"agent_id"`
}

// KillAgentRequest is the kill_agent request payload.
type KillAgentRequest struct {
	AgentID      uuid.UUID `json:"agent_id"`
	GraceSeconds int       `json:"grace_seconds,omitempty"`
}

// KillAgentResponse is the kill_agent reply payload.
type KillAgentResponse struct {
	OK bool `json:"ok"`
}

// ArchiveAgentRequest is the archive_agent request payload. Archiving
// transitions the agent's lifecycle to "archived" — the only state per
// architecture.md §2 that releases the agent's name back into the
// uniqueness pool. When the agent has a live incarnation, the handler
// stops the container first (mirroring kill_agent) so the operator
// doesn't have to issue a kill+archive pair to clean up. Archiving an
// already-archived agent is a no-op success.
type ArchiveAgentRequest struct {
	AgentID      uuid.UUID `json:"agent_id"`
	GraceSeconds int       `json:"grace_seconds,omitempty"`
}

// ArchiveAgentResponse is the archive_agent reply payload.
type ArchiveAgentResponse struct {
	OK bool `json:"ok"`
}

// PromptAgentRequest is the prompt_agent request payload.
type PromptAgentRequest struct {
	AgentID uuid.UUID `json:"agent_id"`
	Content string    `json:"content"`
}

// PromptAgentResponse is the prompt_agent reply payload.
type PromptAgentResponse struct {
	OK bool `json:"ok"`
}

// RestartAgentRequest is the restart_agent request payload. The handler
// stops the live incarnation (if any) and spawns a fresh one against the
// same AgentDefinition. PreserveSession is reserved — M12 records the
// flag but does not yet wire session continuity (no driver loop ships
// in phase 1).
type RestartAgentRequest struct {
	AgentID         uuid.UUID `json:"agent_id"`
	PreserveSession bool      `json:"preserve_session,omitempty"`
}

// RestartAgentResponse is the restart_agent reply payload. AgentID
// echoes the request so a CLI caller can confirm the same agent came
// back — a fresh incarnation lives behind the same UUID.
type RestartAgentResponse struct {
	AgentID uuid.UUID `json:"agent_id"`
	OK      bool      `json:"ok"`
}

// ListDirRequest is the list_dir request payload.
type ListDirRequest struct {
	AgentID uuid.UUID `json:"agent_id"`
	Path    string    `json:"path"`
}

// ListDirEntry is one row of a list_dir response.
//
// Size and Mode are populated best-effort; on a non-stat'able entry
// they are zero-valued.
type ListDirEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
	Mode  string `json:"mode,omitempty"`
}

// ListDirResponse is the list_dir reply payload.
type ListDirResponse struct {
	Entries []ListDirEntry `json:"entries"`
}

// StatRequest is the stat request payload.
type StatRequest struct {
	AgentID uuid.UUID `json:"agent_id"`
	Path    string    `json:"path"`
}

// StatResponse is the stat reply payload.
type StatResponse struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Mode  string `json:"mode"`
	IsDir bool   `json:"is_dir"`
}

// ExecInContainerRequest is the exec_in_container request payload.
// Cmd's first element is the executable; subsequent elements are
// passed as argv to the docker exec call.
type ExecInContainerRequest struct {
	AgentID uuid.UUID         `json:"agent_id"`
	Cmd     []string          `json:"cmd"`
	Workdir string            `json:"workdir,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// ExecInContainerResponse is the exec_in_container reply payload.
// Stdout and Stderr are the captured streams (utf-8 best-effort);
// ExitCode is the process exit status the container reported.
type ExecInContainerResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

// QueryAuditRequest is the query_audit request payload.
type QueryAuditRequest struct {
	Filter    QueryAuditFilter `json:"filter"`
	TimeRange TimeRange        `json:"time_range"`
	Limit     int              `json:"limit,omitempty"`
}

// QueryAuditFilter narrows the ClickHouse audit query. Empty
// strings / nil UUIDs are treated as "any".
type QueryAuditFilter struct {
	Actor     string    `json:"actor,omitempty"`
	Action    string    `json:"action,omitempty"`
	AgentUUID uuid.UUID `json:"agent_uuid,omitempty"`
}

// QueryAuditRow projects one row of the ClickHouse audit table.
// Payload is the raw JSON blob — the audit envelope payload at the
// time of insert. Callers that want the structured AuditPayload
// shape can json.Unmarshal it themselves.
type QueryAuditRow struct {
	ID                 uuid.UUID `json:"id"`
	Ts                 time.Time `json:"ts"`
	Actor              string    `json:"actor"`
	AgentUUID          uuid.UUID `json:"agent_uuid"`
	Action             string    `json:"action"`
	CapabilityRequired string    `json:"capability_required"`
	Result             string    `json:"result"`
	Payload            string    `json:"payload"`
}

// QueryAuditResponse is the query_audit reply payload. Rows is
// always non-nil; empty slice when no rows match.
type QueryAuditResponse struct {
	Rows []QueryAuditRow `json:"rows"`
}

// QueryTraceRequest is the query_trace request payload.
type QueryTraceRequest struct {
	TraceID string `json:"trace_id"`
}

// TraceSpan projects one row of the ClickHouse telemetry_traces
// table. Attributes is the SpanAttributes column; resource
// attributes are intentionally dropped for the M12 CLI surface — the
// trace-tree projection only needs span-local context.
type TraceSpan struct {
	TraceID       string            `json:"trace_id"`
	SpanID        string            `json:"span_id"`
	ParentSpanID  string            `json:"parent_span_id,omitempty"`
	SpanName      string            `json:"span_name"`
	SpanKind      string            `json:"span_kind"`
	ServiceName   string            `json:"service_name"`
	Timestamp     time.Time         `json:"timestamp"`
	DurationNanos int64             `json:"duration_nanos"`
	StatusCode    string            `json:"status_code,omitempty"`
	StatusMessage string            `json:"status_message,omitempty"`
	Attributes    map[string]string `json:"attributes,omitempty"`
}

// QueryTraceResponse is the query_trace reply payload. Spans is
// ordered by Timestamp ASC — the CLI projects this into a tree
// keyed by ParentSpanID.
type QueryTraceResponse struct {
	Spans []TraceSpan `json:"spans"`
}

// WorktreeStatus enumerates the worktree lifecycle states. Mirrors
// specs/protocols/rpc-catalog.md §"Verb payloads — M14 additions"
// `WorktreeInfo.Status`.
type WorktreeStatus string

const (
	WorktreeStatusActive   WorktreeStatus = "active"
	WorktreeStatusMerging  WorktreeStatus = "merging"
	WorktreeStatusMerged   WorktreeStatus = "merged"
	WorktreeStatusConflict WorktreeStatus = "conflict"
	WorktreeStatusArchived WorktreeStatus = "archived"
)

// WorktreeInfo is the per-worktree registry shape. Mirrors the row
// stored under `worktrees.<name>` in NATS KV.
type WorktreeInfo struct {
	Name         string         `json:"name"`
	Path         string         `json:"path"`
	Branch       string         `json:"branch"`
	BaseBranch   string         `json:"base_branch"`
	OwningAgent  uuid.UUID      `json:"owning_agent,omitempty"`
	Status       WorktreeStatus `json:"status"`
	CreatedAt    time.Time      `json:"created_at"`
	LastActivity time.Time      `json:"last_activity"`
}

// WorktreeCreateRequest is the worktree_create request payload.
// BaseBranch defaults to "main" when empty.
type WorktreeCreateRequest struct {
	Name       string `json:"name"`
	BaseBranch string `json:"base_branch,omitempty"`
}

// WorktreeCreateResponse is the worktree_create reply payload.
type WorktreeCreateResponse struct {
	Worktree WorktreeInfo `json:"worktree"`
}

// WorktreeDestroyRequest is the worktree_destroy request payload.
// Force=true is the operator-only override: destroy even when status
// isn't archived. Agent callers (control.worktree cap) get the
// default-false path.
type WorktreeDestroyRequest struct {
	Name  string `json:"name"`
	Force bool   `json:"force,omitempty"`
}

// WorktreeDestroyResponse is the worktree_destroy reply payload.
type WorktreeDestroyResponse struct {
	OK bool `json:"ok"`
}

// WorktreeListRequest is the worktree_list request payload. Empty
// today; reserved for future filters.
type WorktreeListRequest struct{}

// WorktreeListResponse is the worktree_list reply payload. Worktrees
// is always non-nil; empty slice when none exist.
type WorktreeListResponse struct {
	Worktrees []WorktreeInfo `json:"worktrees"`
}

// WorktreeMergeRequest is the worktree_merge request payload.
// Target defaults to "main" when empty.
type WorktreeMergeRequest struct {
	Name   string `json:"name"`
	Target string `json:"target,omitempty"`
}

// WorktreeMergeResponse is the worktree_merge reply payload. OK is
// true on a clean merge; on conflicts OK is false and Conflicts
// lists the file paths git reported as unmerged.
type WorktreeMergeResponse struct {
	OK        bool     `json:"ok"`
	Branch    string   `json:"branch,omitempty"`
	Target    string   `json:"target,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
}

// WorktreeDiffRequest is the worktree_diff request payload.
// Against defaults to "main" when empty.
type WorktreeDiffRequest struct {
	Name    string `json:"name"`
	Against string `json:"against,omitempty"`
}

// WorktreeDiffResponse is the worktree_diff reply payload.
type WorktreeDiffResponse struct {
	Diff string `json:"diff"`
}

// GetVersionRequest is the get_version request payload. The verb takes
// no arguments today; the struct exists so a future bump can add fields
// without breaking wire shape.
type GetVersionRequest struct{}

// GetVersionResponse is the get_version reply payload. The shape lets
// `sextant doctor` print a one-line CLI/daemon comparison and warn when
// the operator forgot to restart the daemon after `make install`.
//
// DaemonVersion mirrors `pkg/version.Version` on the daemon side.
// ProtoVersion mirrors `pkg/sextantproto.ProtoVersion`.
// Commit is the short git SHA (`pkg/version.Commit`).
// StartedAt is the daemon process start time (captured in Start).
// PID is the daemon process ID at the time of the call.
type GetVersionResponse struct {
	DaemonVersion string    `json:"daemon_version"`
	Commit        string    `json:"commit"`
	ProtoVersion  string    `json:"proto_version"`
	StartedAt     time.Time `json:"started_at"`
	PID           int64     `json:"pid"`
}
