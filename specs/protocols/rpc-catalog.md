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
| `exec_in_container` | `{ agent_id, cmd, args[] }` | `{ stdout, stderr, exit_code }` | `control.exec` (operator-level) |

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

- **dev**: `read.agents`, `read.history`, `read.debug`, `read.worktrees`, `control.prompt` (limited), `control.worktree`
- **lead**: dev caps + `control.spawn`, `control.kill`, `control.restart`, `control.prompt`
- **ops**: lead caps + `control.exec`, `control.self_update`, `test.provision`
- **operator** (the human via TUI/CLI): all capabilities

Capability descoping (§9c): spawned agent's caps are a subset of spawner's caps. Spawner declares the requested subset; sextantd validates and issues the child JWT accordingly.

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

## Open

- Per-verb detailed schemas (request/response struct fields) — keep here until they grow large enough to warrant own files
- Capability grant/revoke at runtime — out of scope for initial; JWTs are immutable per incarnation
