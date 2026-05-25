# Bus subjects

The canonical NATS subject layout. Subjects are dot-separated. Wildcards: `*` matches one token, `>` matches one or more. Mirror of `specs/protocols/bus-subjects.md`, adjusted to what `pkg/natsboot/layout.go` actually creates today.

## Namespaces

| Namespace          | Purpose                                            | Where retained                     |
|--------------------|----------------------------------------------------|------------------------------------|
| `agents.*`         | Per-agent events                                   | JetStream + ClickHouse             |
| `audit.>`          | Auth-relevant actions                              | JetStream (365 d) + ClickHouse     |
| `telemetry.>`      | OTel traces / metrics / logs                       | JetStream + ClickHouse             |
| `user_input.>`     | User-input request / response flow                 | JetStream (30 d) + ClickHouse      |
| `sextant.rpc.>`    | RPC request / reply                                | JetStream (24 h)                   |
| `sextant.system.>` | Daemon self-events                                 | JetStream (30 d) + ClickHouse      |
| `ui.state.*`       | Inter-UI coordination (NATS KV, not a stream)      | KV `ui_state`                      |

## `agents.<uuid>.*`

Per-agent subjects.

| Subject                     | Publisher                          | Subscribers                              |
|-----------------------------|------------------------------------|------------------------------------------|
| `agents.<uuid>.frames`      | sidecar                            | operator CLI/TUI, shipper                |
| `agents.<uuid>.lifecycle`   | sidecar + spawn handler            | operator CLI/TUI, shipper                |
| `agents.<uuid>.heartbeat`   | sidecar (every 5s)                 | operator CLI/TUI, shipper                |
| `agents.<uuid>.inbox`       | operator (prompt), MCP `send_message`/`prompt_agent` | sidecar                  |
| `agents.<uuid>.debug`       | sidecar (opt-in verbose telemetry) | operator                                 |

Wildcards in practice:

- `agents.<uuid>.>` — everything about one agent.
- `agents.*.lifecycle` — all lifecycle events (good firehose for a UI).
- `agents.*.frames` — all conversation frames.

## `audit.*`

Auth-relevant actions. Each envelope carries an `AuditPayload`. 365-day retention in JetStream; long-term in ClickHouse `audit` table.

| Subject                | Emitted by                                          |
|------------------------|-----------------------------------------------------|
| `audit.spawn`          | spawn handler                                       |
| `audit.kill`           | kill handler                                        |
| `audit.restart`        | restart handler                                     |
| `audit.archive`        | archive handler                                     |
| `audit.definition_change` | (template change paths)                         |
| `audit.tool_call`      | MCP server, every tool invocation                   |
| `audit.deploy`         | (M16) self-update events                            |
| `audit.access`         | operator CLI/TUI actions                             |

`audit.tool_call` envelope details documented at `specs/protocols/bus-subjects.md:39-49`:

```json
{
  "actor": "<agent UUID or 'operator'>",
  "agent_uuid": "<UUID, when caller is an agent>",
  "action": "tool_call.<tool_name>",
  "capability_required": "<cap string or empty for operator>",
  "result": "allowed" | "denied" | "error",
  "details": {
    "tool": "send_message",
    "caller_kind": "agent" | "operator",
    "caller_id": "<UUID or 'operator'>",
    "duration_ms": 12,
    "error_code": "capability_denied"   // when result != "allowed"
  }
}
```

## `telemetry.*`

OTel-shaped signals.

| Subject                     | Payload kind        |
|-----------------------------|---------------------|
| `telemetry.traces.<host>`   | `telemetry_span`    |
| `telemetry.metrics.<host>`  | `telemetry_metric`  |
| `telemetry.logs.<host>`     | `telemetry_log`     |

## `user_input.*`

The user-input propagation pattern (architecture §4a). The wire shape is implemented; the "layered review / batching" UX is not yet built.

- `user_input.requests.<from_uuid>` — an agent asking for input.
- `user_input.responses.<request_id>` — answer / escalate / defer.

## `sextant.rpc.*`

One subject per RPC verb. Caller publishes a request to `sextant.rpc.<verb>` with `reply_to` set; `sextantd` publishes the response on the reply subject.

Implemented verbs at this snapshot (see [RPC catalog](./rpc-catalog.md)):

```
sextant.rpc.list_agents
sextant.rpc.get_agent_status
sextant.rpc.spawn_agent
sextant.rpc.kill_agent
sextant.rpc.restart_agent
sextant.rpc.prompt_agent
sextant.rpc.archive_agent
sextant.rpc.read_file
sextant.rpc.list_dir
sextant.rpc.stat
sextant.rpc.exec_in_container
sextant.rpc.query_history
sextant.rpc.query_audit
sextant.rpc.query_trace
sextant.rpc.worktree_create
sextant.rpc.worktree_destroy
sextant.rpc.worktree_list
sextant.rpc.worktree_merge
sextant.rpc.worktree_diff
```

Subjects in `specs/protocols/bus-subjects.md` for `read_file_stream`, `trigger_thought_dump`, `enable_verbose_logging`, and `self_update` are spec-only — they do not exist on the bus at this snapshot. See [Known gaps and drift](../reference/known-gaps.md).

## `sextant.system.*`

Daemon self-management namespace. The `system` JetStream stream covers `sextant.system.>` with 30-day retention (`pkg/natsboot/layout.go:109-115`). The only active code path publishing into it at this snapshot is the MCP `emit_event` tool (`pkg/mcpserver/server.go:836`), which accepts an operator-supplied sub-subject. Spec names like `sextant.system.daemon_started`, `daemon_shutdown`, `subprocess_restart`, and `self_update_*` are reserved but not yet produced.

## `ui.state.*`

This is **NATS KV**, not a stream. Bucket `ui_state`. Keys are operator-scoped: `<operator>.<field>`.

| Key                          | Value                          |
|------------------------------|--------------------------------|
| `<operator>.selected_agent`  | Agent UUID, or `"none"`        |
| `<operator>.focused_pane`    | Opaque TUI-defined string      |
| `<operator>.filter`          | Filter DSL (out of scope today) |

TUIs subscribe to relevant keys and react when they change. The TUI that *owns* selection writes the key.

> **Note on naming**: the legacy `ui.state.<operator>.<field>` form refers to the same thing as `<operator>.<field>` — the `ui.state` prefix is implicit in the bucket. The on-the-wire key is `<operator>.<field>`. See `conventions/tui-conventions.md`.

## Subject ACLs

Per-agent JWTs carry subject allowlists. Default agent allowance:

- Publish: `agents.<own_uuid>.*`, `user_input.requests.<own_uuid>`, `user_input.responses.*`.
- Subscribe: `agents.<own_uuid>.inbox`, plus anything declared by capability.

Operator connections use the `operator.creds` NATS user, which has full publish/subscribe authority on every subject. The trust boundary is Unix permissions on the creds file, not a NATS-level ACL.

> **M11 transitional note**: sidecars currently connect to NATS as the *operator* user (using the env-var creds) rather than under their own NATS user. The per-agent JWT is consumed only by the MCP server, not by NATS. Per-NATS-user JWTs are a future hardening pillar.
