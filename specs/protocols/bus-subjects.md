# Bus subjects — protocol spec

Canonical NATS subject layout for sextant initial. Subjects are dot-separated hierarchies. Wildcards: `*` matches one token, `>` matches one or more.

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
- `audit.tool_call` — MCP tool invoked
- `audit.deploy` — self_update events
- `audit.access` — operator action (CLI/TUI)

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

Operator: connects via the Unix socket listener; no JWT and no subject ACL — full publish/subscribe authority is implicit from socket access (see `architecture.md` §10b). NATS-level ACLs apply only to agents on the TCP listener.

Cross-agent send_message is implemented via the MCP tool which validates capability before publishing — agents don't get raw publish access to other agents' subjects.

## Open

- Should `audit.*` have finer sub-namespacing? E.g. `audit.tool_call.<tool_name>` for easier filter
- KV bucket naming convention — see `nats.md` for the buckets; this doc focuses on streams
- Per-host scoping in subjects — needed for multi-host (`agents.<host>.<uuid>.*`?) or carry in envelope metadata? Lean: in envelope. Subjects stay flat.
