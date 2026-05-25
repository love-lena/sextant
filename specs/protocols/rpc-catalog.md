# RPC catalog — protocol spec

Every "do this now" or "fetch that now" operation is an RPC. RPCs travel over NATS request/reply on subjects `sextant.rpc.<verb>`.

Each RPC has: a verb, a request schema, a response schema, a required capability.

This document is the catalog; per-verb detailed specs may live in companion files for the more complex ones.

## Conventions

- Request and response are JSON, wrapped in an `Envelope` (see `envelope-schema.md`).
- Every request carries an `idempotency_key` (UUID) — sextantd dedupes within a 60s window.
- Every request carries `reply_to` — the NATS subject sextantd publishes the response to.
- Default timeout: 10 seconds. Override via client option.
- Streaming responses use an ephemeral subject (the client creates a sub, server publishes multiple messages to it).
- Errors are returned as structured `rpc_response` envelopes with `error` field, not by failing to reply.

## Catalog by category

### Agent state queries

| Verb | Request | Response | Capability |
|---|---|---|---|
| `list_agents` | `{ filter?: {} }` | `{ agents: AgentSummary[] }` | `read.agents` |
| `get_agent_status` | `{ agent_id: UUID }` | `{ status: AgentStatus }` | `read.agents` |
| `get_session_summary` | `{ agent_id: UUID }` | `{ session: SessionSummary }` | `read.agents` |
| `query_history` | `{ filter: QueryFilter, time_range: TimeRange }` | `{ events: Envelope[] }` | `read.history` |
| `query_audit` | `{ filter: AuditFilter, time_range: TimeRange }` | `{ rows: AuditRow[] }` | `read.history` |
| `query_trace` | `{ trace_id: string }` | `{ spans: TraceSpan[] }` | `read.history` |

### Agent lifecycle

| Verb | Request | Response | Capability |
|---|---|---|---|
| `spawn_agent` | `{ name, template, host_pin?, definition_overrides? }` | `{ agent_id }` | `control.spawn` |
| `kill_agent` | `{ agent_id, grace_seconds? }` | `{ ok: bool }` | `control.kill` |
| `restart_agent` | `{ agent_id, preserve_session?: bool }` | `{ ok: bool }` | `control.restart` |
| `prompt_agent` | `{ agent_id, content }` | `{ ok: bool }` | `control.prompt` |
| `archive_agent` | `{ agent_id }` | `{ ok: bool }` | `control.archive` |

### Container/filesystem access

| Verb | Request | Response | Capability |
|---|---|---|---|
| `read_file` | `{ agent_id, path }` | `{ content: bytes, content_type }` | `read.container_files` |
| `list_dir` | `{ agent_id, path }` | `{ entries: DirEntry[] }` | `read.container_files` |
| `stat` | `{ agent_id, path }` | `{ meta }` | `read.container_files` |
| `read_file_stream` | `{ agent_id, path, follow }` | streaming response | `read.container_files` |
| `exec_in_container` | `{ agent_id, cmd: string[], workdir?, env? }` | `{ stdout, stderr, exit_code }` | `control.exec` (operator-level) |

### On-demand telemetry

| Verb | Request | Response | Capability |
|---|---|---|---|
| `trigger_thought_dump` | `{ agent_id }` | `{ ok: bool }` | `read.debug` |
| `enable_verbose_logging` | `{ agent_id, duration_seconds }` | `{ ok: bool, expires_at }` | `read.debug` |

### Worktrees

| Verb | Request | Response | Capability |
|---|---|---|---|
| `worktree_create` | `{ name, base_branch? }` | `{ path }` | `control.worktree` |
| `worktree_destroy` | `{ name }` | `{ ok: bool }` | `control.worktree` (operator-level) |
| `worktree_list` | `{}` | `{ worktrees: WorktreeInfo[] }` | `read.worktrees` |
| `worktree_merge` | `{ name, target?: "main" }` | `{ ok: bool, conflicts?: ConflictReport }` | `control.worktree` |
| `worktree_diff` | `{ name, against?: "main" }` | `{ diff }` | `read.worktrees` |

### Self-update

| Verb | Request | Response | Capability |
|---|---|---|---|
| `self_update` | `{ target_revision }` | `{ deploy_id, status: "staging" }` | `control.self_update` (operator-level) |
| `self_rollback` | `{}` | `{ ok: bool }` | `control.self_update` |
| `query_deploy_history` | `{ time_range }` | `{ deploys: DeployRecord[] }` | `read.deploys` |

### Test environments

| Verb | Request | Response | Capability |
|---|---|---|---|
| `provision_test_environment` | `{ name?, ttl_minutes?, profile? }` | `{ test_id, nats_url, clickhouse_url, test_ca_pubkey, config_path }` | `test.provision` |
| `teardown_test_environment` | `{ test_id }` | `{ ok: bool }` | `test.provision` |
| `list_test_environments` | `{}` | `{ envs: TestEnvInfo[] }` | `test.provision` |
| `connect_to_test_environment` | `{ test_id }` | `{ connection_config }` | `test.provision` |

## Capability mapping

Capabilities are hierarchical strings; granting `read.>` grants all `read.*` capabilities.

Default capability sets per agent type (declared in templates):

- **dev**: `read.agents`, `read.history`, `read.debug`, `read.worktrees`, `control.prompt` (limited), `control.worktree`, `send_message`
- **lead**: dev caps + `control.spawn`, `control.kill`, `control.restart`, `control.prompt`, `broadcast`
- **ops**: lead caps + `control.exec`, `control.self_update`, `test.provision`
- **operator** (the human via TUI/CLI): all capabilities

Capability descoping (§9c): spawned agent's caps are a subset of spawner's caps. Spawner declares the requested subset; sextantd validates and issues the child JWT accordingly.

### MCP tool capabilities (M10+)

The MCP server (`specs/components/sextantd.md` §"MCP server") exposes a separate tool catalog with its own capability strings. The mapping below is normative; tools whose semantics overlap an existing RPC verb reuse that verb's cap.

| Tool | Capability |
|---|---|
| `send_message` | `send_message` |
| `broadcast` | `broadcast` |
| `list_agents` | `read.agents` |
| `agent_status` | `read.agents` |
| `query_audit` | `read.history` |
| `spawn_agent` | `control.spawn` |
| `kill_agent` | `control.kill` |
| `prompt_agent` | `control.prompt` |
| `emit_event` | `emit_event` |
| `get_metric` | `read.metrics` |

## Wire semantics

This section is normative for the M7 RPC server and every client implementation.

### Request envelope

- Subject: `sextant.rpc.<verb>`
- Envelope `kind`: `rpc_request`
- Required fields: `id`, `ts`, `from`, `idempotency_key`, `reply_to`, `kind`, `payload`
- `reply_to` is a caller-provisioned ephemeral subject. Caller subscribes to it before publishing the request.
- `payload` is the verb-specific request shape (see catalog tables above).

### Response envelope

- Subject: the `reply_to` from the request
- Envelope `kind`: `rpc_response`
- `payload` shape:
  ```go
  type RPCResponse struct {
      Result   json.RawMessage `json:"result,omitempty"`  // verb-specific reply shape; absent on error
      Error    *RPCError       `json:"error,omitempty"`   // structured error; absent on success
      Terminal bool            `json:"_terminal"`         // true on the final response envelope
  }
  ```
- Single-reply RPCs return exactly one response envelope with `_terminal: true`.
- Streaming RPCs publish multiple response envelopes on the same `reply_to`; all but the last have `_terminal: false`; the last has `_terminal: true` (and may carry a final summary in `result`).

### Errors

```go
type RPCError struct {
    Code    string         `json:"code"`              // stable identifier, e.g. "agent_not_found", "capability_denied", "timeout"
    Message string         `json:"message"`           // human-readable
    Details map[string]any `json:"details,omitempty"` // verb-specific structured detail
}
```

Errors are always returned as `rpc_response` envelopes with `error` set and `_terminal: true`. Never by failing to reply — a missing reply is a protocol violation, not an error.

### Timeouts

- Default request timeout: 10s on the client side.
- Override via `WithTimeout` client option (Go) or `RPCOptions.timeoutMs` (TS).
- On timeout, the client unsubscribes from `reply_to` and returns a synthetic `RPCError{Code: "timeout"}`. The server may still publish a late response; it lands in nothing.

### Cancellation (streaming)

- Caller cancels by unsubscribing from `reply_to`.
- Server detects the no-subscribers state via NATS' `no_responders` / `no_subscribers` signal (or by checking subscriber count before each publish) and stops emitting.
- Servers must not assume the stream completes; partial streams are normal.

### Idempotency

- Every request carries `idempotency_key` (UUID, caller-generated).
- Server caches `(verb, idempotency_key) → response_envelope` for 60s.
- Repeat requests within that window return the cached response without re-executing.
- After the 60s window the cache entry expires and the same key would re-execute.
- The server enforces a max-entries cap on the cache (default 10000) to bound memory under a bursty client. Eviction is by expiry order — expired entries go first, then the entry with the soonest expiry. A re-Store of an existing key extends its lifetime without counting against the cap. Clients should accept that a sufficiently-old replay against a server under heavy load may re-execute even inside the 60s window.

### Audit

- Before dispatch, server emits one `audit.rpc` envelope: `{verb, from, idempotency_key, capability_required, allowed: bool}`.
- After completion (success, error, or timeout), server emits one `audit.rpc_result`: `{idempotency_key, terminal_reason: "success" | "error" | "stream_canceled", duration_ms, error_code?}`.
- Both audit envelopes share the same `trace_id` as the original request for cross-reference.

### Server dispatch (Go sketch)

```go
type Handler func(ctx context.Context, req Envelope, emit func(RPCResponse)) error

type Server struct {
    handlers map[string]Handler  // verb → handler
    // ...
}

func (s *Server) dispatch(ctx context.Context, msg Message) {
    req := msg.Envelope
    verb := strings.TrimPrefix(req.Subject, "sextant.rpc.")
    h, ok := s.handlers[verb]
    if !ok {
        s.replyError(req, "unknown_verb", fmt.Sprintf("no handler for %q", verb))
        return
    }
    if err := s.checkCap(req, verb); err != nil {
        s.replyError(req, "capability_denied", err.Error())
        return
    }
    // ... idempotency check, audit, run handler with emit callback, audit again
}
```

## Verb payloads — M7 initial set

These are the request/response payload shapes for the four verbs implemented in M7. JSON tags shown match the wire form. Times are RFC 3339 strings; UUIDs are lowercase canonical strings (matching `sextantproto`'s wire format).

### `list_agents`

Request:
```go
type ListAgentsRequest struct {
    Filter *ListAgentsFilter `json:"filter,omitempty"` // optional; reserved for future filtering
}

type ListAgentsFilter struct {
    Lifecycle string `json:"lifecycle,omitempty"` // "defined" | "running" | "paused" | "archived"
}
```

Response:
```go
type ListAgentsResponse struct {
    Agents []AgentSummary `json:"agents"` // always present; empty slice when none match
}

type AgentSummary struct {
    UUID      uuid.UUID `json:"uuid"`
    Name      string    `json:"name"`
    Type      string    `json:"type,omitempty"`
    Template  string    `json:"template,omitempty"`
    Lifecycle string    `json:"lifecycle"`
    Version   uint64    `json:"version"`
    UpdatedAt time.Time `json:"updated_at"`
}
```

### `get_agent_status`

Request:
```go
type GetAgentStatusRequest struct {
    AgentID uuid.UUID `json:"agent_id"`
}
```

Response:
```go
type GetAgentStatusResponse struct {
    Status AgentStatus `json:"status"`
}

type AgentStatus struct {
    UUID      uuid.UUID `json:"uuid"`
    Name      string    `json:"name"`
    Lifecycle string    `json:"lifecycle"`
    Version   uint64    `json:"version"`
    UpdatedAt time.Time `json:"updated_at"`
}
```

Error: `RPCError{Code: "agent_not_found"}` when no `agent_definitions.<uuid>` KV entry exists.

### `read_file`

Request:
```go
type ReadFileRequest struct {
    AgentID uuid.UUID `json:"agent_id"`
    Path    string    `json:"path"`
}
```

Response:
```go
type ReadFileResponse struct {
    Content     []byte `json:"content"`      // base64 on the wire (json.RawMessage of bytes)
    ContentType string `json:"content_type"` // sniffed MIME
}
```

M7 status: STUB. The M7 handler always returns `RPCError{Code: "not_implemented"}` with the message "read_file ships in M11+ when container management lands". The verb is registered so unknown-verb path doesn't fire, but the body is intentionally not implemented.

### `query_history`

Request:
```go
type QueryHistoryRequest struct {
    Filter    QueryHistoryFilter `json:"filter"`
    TimeRange TimeRange          `json:"time_range"`
    Limit     int                `json:"limit,omitempty"` // 0 → server default (1000); capped at server max (10000)
}

type QueryHistoryFilter struct {
    Subject   string    `json:"subject,omitempty"`    // optional exact match on subject column
    FromID    string    `json:"from_id,omitempty"`    // optional exact match on the from.id column
    AgentUUID uuid.UUID `json:"agent_uuid,omitempty"` // optional; matches from.id when from.kind = "agent"
    Kind      string    `json:"kind,omitempty"`       // optional envelope kind filter
}

type TimeRange struct {
    Since time.Time `json:"since,omitempty"` // zero = unbounded
    Until time.Time `json:"until,omitempty"` // zero = unbounded (i.e. now)
}
```

Response:
```go
type QueryHistoryResponse struct {
    Events []sextantproto.Envelope `json:"events"` // always present; empty slice when none match
}
```

The handler queries the ClickHouse `events` table; rows are reconstructed into `Envelope` values (Subject is dropped — it is not an envelope field). Wildcard matching on `subject` is intentionally NOT supported in M7 — the filter is exact match; wildcard support lands when a real consumer needs it.

## Verb payloads — M12 additions

These extend the M7 set. Pinned for M12 (CLI surface).

### `restart_agent`

```go
type RestartAgentRequest struct {
    AgentID         uuid.UUID `json:"agent_id"`
    PreserveSession bool      `json:"preserve_session,omitempty"`
}
type RestartAgentResponse struct {
    AgentID uuid.UUID `json:"agent_id"` // same UUID; new incarnation behind it
    OK      bool      `json:"ok"`
}
```

The handler kills the current incarnation (if any), then spawns a fresh one against the same `AgentDefinition`. `PreserveSession` is recorded for future use — M12 doesn't yet persist session state across incarnations, so the flag is accepted but has no effect today.

### `read_file` / `list_dir` / `stat` / `exec_in_container`

```go
type ReadFileRequest struct {
    AgentID uuid.UUID `json:"agent_id"`
    Path    string    `json:"path"`
}
type ReadFileResponse struct {
    Content     []byte `json:"content"`      // base64 on the wire
    ContentType string `json:"content_type"` // sniffed MIME
}

type ListDirRequest struct {
    AgentID uuid.UUID `json:"agent_id"`
    Path    string    `json:"path"`
}
type ListDirEntry struct {
    Name  string `json:"name"`
    IsDir bool   `json:"is_dir"`
    Size  int64  `json:"size,omitempty"`
    Mode  string `json:"mode,omitempty"`
}
type ListDirResponse struct {
    Entries []ListDirEntry `json:"entries"`
}

type StatRequest struct {
    AgentID uuid.UUID `json:"agent_id"`
    Path    string    `json:"path"`
}
type StatResponse struct {
    Name  string `json:"name"`
    Size  int64  `json:"size"`
    Mode  string `json:"mode"`
    IsDir bool   `json:"is_dir"`
}

type ExecInContainerRequest struct {
    AgentID uuid.UUID `json:"agent_id"`
    Cmd     []string  `json:"cmd"` // first element is the executable
    Workdir string    `json:"workdir,omitempty"`
    Env     map[string]string `json:"env,omitempty"`
}
type ExecInContainerResponse struct {
    Stdout   string `json:"stdout"`
    Stderr   string `json:"stderr"`
    ExitCode int    `json:"exit_code"`
}
```

All four execute via `docker exec` against the live incarnation's container. `read_file_stream` is deferred to a follow-up — M12 ships `files tail` by polling `read_file` on a small interval (file end detection by size comparison).

### `query_audit`

```go
type QueryAuditRequest struct {
    Filter    QueryAuditFilter `json:"filter"`
    TimeRange TimeRange        `json:"time_range"`
    Limit     int              `json:"limit,omitempty"`
}
type QueryAuditFilter struct {
    Actor     string    `json:"actor,omitempty"`
    Action    string    `json:"action,omitempty"`
    AgentUUID uuid.UUID `json:"agent_uuid,omitempty"`
}
type QueryAuditRow struct {
    ID                 uuid.UUID `json:"id"`
    Ts                 time.Time `json:"ts"`
    Actor              string    `json:"actor"`
    AgentUUID          uuid.UUID `json:"agent_uuid"`
    Action             string    `json:"action"`
    CapabilityRequired string    `json:"capability_required"`
    Result             string    `json:"result"`
    Payload            string    `json:"payload"` // raw JSON string
}
type QueryAuditResponse struct {
    Rows []QueryAuditRow `json:"rows"`
}
```

Limit defaults to `QueryHistoryDefaultLimit` (1000) and is capped at `QueryHistoryMaxLimit` (10000). Matches the existing `query_history` clamps so operators can predict the surface across both verbs.

### `query_trace`

```go
type QueryTraceRequest struct {
    TraceID string `json:"trace_id"`
}
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
type QueryTraceResponse struct {
    Spans []TraceSpan `json:"spans"` // ordered by Timestamp ASC
}
```

Returns every span in `telemetry_traces` with the supplied TraceId. The CLI's `traces show` projects these into a tree by ParentSpanID.

## Verb payloads — M14 additions

These extend the M12 set. Pinned for M14 (worktree management).

```go
type WorktreeCreateRequest struct {
    Name       string `json:"name"`
    BaseBranch string `json:"base_branch,omitempty"` // default "main"
}
type WorktreeInfo struct {
    Name         string    `json:"name"`
    Path         string    `json:"path"`
    Branch       string    `json:"branch"`
    BaseBranch   string    `json:"base_branch"`
    OwningAgent  uuid.UUID `json:"owning_agent,omitempty"` // zero uuid = operator-created
    Status       string    `json:"status"`                  // active|merging|merged|conflict|archived
    CreatedAt    time.Time `json:"created_at"`
    LastActivity time.Time `json:"last_activity"`
}
type WorktreeCreateResponse struct {
    Worktree WorktreeInfo `json:"worktree"`
}

type WorktreeDestroyRequest struct {
    Name  string `json:"name"`
    Force bool   `json:"force,omitempty"` // operator override: destroy even if status != archived
}
type WorktreeDestroyResponse struct {
    OK bool `json:"ok"`
}

type WorktreeListRequest struct{}
type WorktreeListResponse struct {
    Worktrees []WorktreeInfo `json:"worktrees"`
}

type WorktreeMergeRequest struct {
    Name   string `json:"name"`
    Target string `json:"target,omitempty"` // default "main"
}
type WorktreeMergeResponse struct {
    OK        bool             `json:"ok"`
    Branch    string           `json:"branch,omitempty"`
    Target    string           `json:"target,omitempty"`
    Conflicts []string         `json:"conflicts,omitempty"` // file paths in conflict
}

type WorktreeDiffRequest struct {
    Name    string `json:"name"`
    Against string `json:"against,omitempty"` // default "main"
}
type WorktreeDiffResponse struct {
    Diff string `json:"diff"`
}
```

`WorktreeInfo.Status` values:

- `active` — branch is in progress; the source-of-truth state for a freshly-created worktree.
- `merging` — set while the merge handler holds `locks.merge` for this worktree.
- `merged` — set on a successful merge; the worktree dir may still exist (the operator chooses when to `worktree_destroy`).
- `conflict` — set when the most recent merge attempt produced conflicts. The worktree files are unchanged from before the attempt.
- `archived` — set when the operator/agent marks the worktree done; it's safe to destroy.

Audit envelope action strings: `worktree_create`, `worktree_destroy`, `worktree_list`, `worktree_merge`, `worktree_diff` — mirrors the verb name.

## Open

- Per-verb detailed schemas (request/response struct fields) — keep here until they grow large enough to warrant own files. The four M7 verbs are pinned in the section above; M12 adds the file/exec/audit/trace verbs in the section above this one; M14 adds the worktree verbs.
- Capability grant/revoke at runtime — out of scope for initial; JWTs are immutable per incarnation
- Wildcard subject filtering in `query_history` — deferred; M7 ships exact-match only
- Streaming `read_file_stream` — deferred to a follow-up; M12 ships `files tail` as a poll loop over `read_file`.
