# Bus subjects — protocol spec

Canonical NATS subject layout for sextant. Subjects are dot-separated hierarchies. Wildcards: `*` matches one token, `>` matches one or more.

## Top-level namespaces

| Namespace | Purpose | Retention |
|---|---|---|
| `agents.*` | per-agent events | varies by kind |
| `audit.*` | auth-relevant actions | 365 days |
| `telemetry.*` | OTel traces/metrics/logs | varies |
| `user_input.*` | user-input request/response flow | 30 days |
| `sextant.rpc.*` | RPC request/reply | 1 day |
| `sextant.system.*` | sextantd self-management | 30 days |
| `ui.state.*` | inter-UI coordination | KV, not stream |

## Detailed subject map

### `agents.<uuid>.*`

Per-agent subjects.

- `agents.<uuid>.frames` — agent SDK output (assistant text, tool calls, etc.)
- `agents.<uuid>.lifecycle` — state transitions (started, ended, paused, etc.)
- `agents.<uuid>.heartbeat` — periodic health beats from the sidecar
- `agents.<uuid>.inbox` — incoming prompts and commands (subscribed by sidecar)
- `agents.<uuid>.debug` — opt-in verbose telemetry (when verbose logging enabled)

### `audit.*`

- `audit.spawn` — agent spawned
- `audit.kill` — agent killed
- `audit.restart` — agent restarted
- `audit.definition_change` — agent definition mutated
- `audit.tool_call` — MCP tool invoked (one envelope per call, success or error)
- `audit.deploy` — self_update events
- `audit.access` — operator action (CLI/TUI)

`audit.tool_call` envelope payload (M10+): the standard `AuditPayload` struct (see `pkg/sextantproto/payloads.go`) with:
- `actor` — agent UUID string for agent callers, literal `"operator"` for stdio (operator) callers
- `agent_uuid` — set when the caller is an agent
- `action` — `"tool_call.<tool_name>"`
- `capability_required` — the capability the tool declares; empty for the operator path
- `result` — `"allowed"` on success, `"denied"` on capability_denied, `"error"` on any other terminal failure
- `details.tool` — the tool name
- `details.caller_kind` — `"agent"` or `"operator"`
- `details.caller_id` — the caller's identity (agent UUID or `"operator"`)
- `details.duration_ms` — wall-clock duration of the handler in milliseconds
- `details.error_code` — present when result != allowed; the tool error code (e.g. `"capability_denied"`, `"not_implemented"`)

### `telemetry.*`

- `telemetry.traces.<host>` — OTel spans
- `telemetry.metrics.<host>` — OTel metrics
- `telemetry.logs.<host>` — OTel log records

### `user_input.*`

- `user_input.requests.<from_uuid>` — agent requesting input
- `user_input.responses.<request_id>` — answer/escalate/defer for a specific request

### `sextant.rpc.*`

One subject per RPC verb. Caller publishes a request with `reply_to`; sextantd handles, publishes response to the reply subject.

- `sextant.rpc.get_agent_status`
- `sextant.rpc.list_agents`
- `sextant.rpc.spawn_agent`
- `sextant.rpc.kill_agent`
- `sextant.rpc.restart_agent`
- `sextant.rpc.prompt_agent`
- `sextant.rpc.read_file`
- `sextant.rpc.list_dir`
- `sextant.rpc.read_file_stream`
- `sextant.rpc.exec_in_container`
- `sextant.rpc.query_history`
- `sextant.rpc.trigger_thought_dump`
- `sextant.rpc.worktree_create`
- `sextant.rpc.worktree_merge`
- ... (full list in `rpc-catalog.md`)

### `sextant.system.*`

- `sextant.system.daemon_started`
- `sextant.system.daemon_shutdown`
- `sextant.system.subprocess_restart`
- `sextant.system.self_update_started`
- `sextant.system.self_update_completed`
- `sextant.system.self_update_failed`

## Wildcard subscription patterns

Common patterns UIs will subscribe to:

- `agents.<uuid>.>` — everything about one agent
- `agents.*.lifecycle` — all lifecycle events
- `agents.*.frames` — every conversation frame (firehose)
- `audit.>` — full audit stream
- `user_input.requests.*` — every input request (for the pending queue)
- `telemetry.traces.>` — all traces

## Subject ACLs

Per-agent JWTs encode subject allowlists. Default agent caps:
- Publish: `agents.<own_uuid>.*`, `user_input.requests.<own_uuid>`, `user_input.responses.*`
- Subscribe: `agents.<own_uuid>.inbox`, anything declared by capability

Operator: connects via the operator NATS user defined in `~/.config/sextant/operator.creds` (Unix file perms = trust boundary; see `architecture.md` §10b and `specs/components/nats.md`). The operator user has full publish/subscribe authority on every subject. NATS-level ACLs apply only to per-agent JWT users.

Cross-agent send_message is implemented via the MCP tool which validates capability before publishing — agents don't get raw publish access to other agents' subjects.

## Open

- Should `audit.*` have finer sub-namespacing? E.g. `audit.tool_call.<tool_name>` for easier filter
- KV bucket naming convention — see `nats.md` for the buckets; this doc focuses on streams
- Per-host scoping in subjects — needed for multi-host (`agents.<host>.<uuid>.*`?) or carry in envelope metadata? Lean: in envelope. Subjects stay flat.
