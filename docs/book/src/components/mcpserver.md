# mcpserver

**Source**: `pkg/mcpserver/`.

The MCP server is the *agent-facing tool catalog*. Agents call its tools to send messages, query state, spawn/kill children, manage worktrees, and emit events. It lives in-process inside `sextantd` and serves two transports: Streamable HTTP for sidecars, stdio (Unix socket) for the operator.

## When to reach for this component

- "Where does an agent's `send_message` actually go?" — start here.
- You're adding a new MCP tool or changing a capability gate.
- You're investigating a `capability_denied` error.
- You want to know which tools an operator can call from the CLI.

## When *not* to reach for this component

If you're calling sextant from Go or TS (a TUI, a CLI script, a custom agent), use the [client library](../client-libraries/go-client.md) RPC layer instead. The MCP server is for the Claude Agent SDK; operators usually go through `pkg/rpc` instead.

## Public surface

| Symbol                              | File:line                       | Purpose                                          |
|-------------------------------------|---------------------------------|--------------------------------------------------|
| `Server`                            | `pkg/mcpserver/server.go:136`   | The MCP server (`Config` struct at line 44).     |
| `New(cfg Config)`                   | `pkg/mcpserver/server.go:161`   | Construct (validates config).                    |
| `Start(ctx)`                        | `pkg/mcpserver/server.go:212`   | Bind listeners synchronously.                    |
| `Run(ctx)`                          | `pkg/mcpserver/server.go:246`   | Start + block until ctx canceled.                |
| `SetSpawnDeps(*SpawnDeps)`          | `pkg/mcpserver/server.go:259`   | Late-bind spawn/kill backend.                    |
| `SetWorktree(handlers.WorktreeManager)` | `pkg/mcpserver/server.go:1034` | Late-bind worktree manager.                 |
| `HTTPURL()`                         | `pkg/mcpserver/server.go:284`   | `http://host:port/mcp` for `SEXTANT_MCP_URL`.    |
| `StdioSocketPath()`                 | `pkg/mcpserver/server.go:294`   | Operator stdio socket path.                      |
| `Close()`                           | `pkg/mcpserver/server.go:302`   | Idempotent shutdown.                             |
| `Caller`                            | `pkg/mcpserver/caller.go:32`    | Per-call identity (operator vs agent + caps).    |
| `AllTools()`                        | `pkg/mcpserver/tools.go:58`     | Stable-order tool catalog.                       |
| `CapForTool(name)`                  | `pkg/mcpserver/tools.go:84`     | Tool → capability mapping.                       |

## Tool catalog

17 tools, all in catalog order at `pkg/mcpserver/tools.go:58-78`:

| Tool                 | Capability           | What it does                                          |
|----------------------|----------------------|-------------------------------------------------------|
| `send_message`       | `send_message`       | Publish a message to `agents.<to_agent>.inbox`.       |
| `broadcast`          | `broadcast`          | Publish to a `broadcast.*` subject.                   |
| `list_agents`        | `read.agents`        | Snapshot of agents matching an optional lifecycle filter. |
| `agent_status`       | `read.agents`        | Detailed status for one agent UUID.                   |
| `query_audit`        | `read.history`       | Query the `audit` ClickHouse table.                   |
| `spawn_agent`        | `control.spawn`      | Spawn an agent from a template.                       |
| `kill_agent`         | `control.kill`       | Stop an agent's container with grace.                 |
| `archive_agent`      | `control.archive`    | Move agent to `archived`; release name. Stops live container too. |
| `prompt_agent`       | `control.prompt`     | Publish a prompt to the agent's inbox.                |
| `emit_event`         | `emit_event`         | Publish under `sextant.system.*`.                     |
| `get_metric`         | `read.metrics`       | Fetch a metric value (windowed).                      |
| `worktree_create`    | `control.worktree`   | Create a worktree + branch.                           |
| `worktree_destroy`   | `control.worktree`   | Remove a worktree.                                    |
| `worktree_list`      | `read.worktrees`     | List the registry.                                    |
| `worktree_merge`     | `control.worktree`   | Merge a worktree's branch into a target.              |
| `worktree_diff`      | `read.worktrees`     | Diff a worktree against another branch.               |
| `templates_reload`   | `control.templates`  | Re-scan `~/.config/sextant/templates/`, push to KV.   |

Capability strings are the same identifiers used by templates' `permissions = [...]` field. A template grants a tool by listing its capability.

## Transport details

### Streamable HTTP — for sidecars

- Listener: `MCPConfig.HTTPHost:HTTPPort` (default `127.0.0.1:5172`).
- Sidecars reach it via `http://host.docker.internal:5172/mcp` (the URL is injected into the container as `SEXTANT_MCP_URL`).
- Auth: JWT in `Authorization: Bearer <token>`. Verified against the CA on every request.
- SSE long-lived connections; graceful shutdown gives them 5s before force-close (`pkg/mcpserver/server.go:394`).

### Stdio over Unix socket — for the operator

- Path: `MCPConfig.StdioSocket` (default `~/.local/share/sextant/sextantd-mcp.sock`, mode 0600).
- No JWT — operator authority is inherited from Unix file permissions on the socket.
- The dispatcher records the caller as `Caller{Kind: CallerOperator}`; `HasCap("...")` always returns true for operators (`pkg/mcpserver/caller.go:42`).
- Accept loop has backoff on transient errors (EMFILE etc.) so a file-descriptor pressure burst doesn't kill the server (`pkg/mcpserver/server.go:498`).

Both transports dispatch to the **same** tool handlers (`pkg/mcpserver/server.go:356`).

## Authorization flow per tool call

1. Transport gives the dispatcher a `mcpauth.TokenInfo` (HTTP) or a synthetic operator token (stdio).
2. `callerFromTokenInfo` extracts the `Caller` (`pkg/mcpserver/auth.go:53`).
3. `wrapHandler` (`pkg/mcpserver/server.go:671`):
   - Looks up the tool's required cap via `CapForTool`.
   - Calls `caller.HasCap(cap)`. On miss, returns `capability_denied`, audits with `result=denied`.
   - Otherwise invokes the typed handler with panic recovery.
4. After the handler returns, the dispatcher publishes an `audit.tool_call` envelope with the result (`allowed`, `denied`, or `error`) and a `duration_ms` measurement.

A panic in a single handler is logged with stack and reported to the caller as `internal`; the server keeps running (`pkg/mcpserver/server.go:736`).

## Audit envelope shape

`audit.tool_call` carries the standard `AuditPayload` plus details (per `specs/protocols/bus-subjects.md`):

- `details.tool` — tool name
- `details.caller_kind` — `agent` or `operator`
- `details.caller_id` — agent UUID, or `"operator"`
- `details.duration_ms` — handler wall-clock duration
- `details.error_code` — present when `result != allowed`

## Test coverage

- `pkg/mcpserver/server_test.go` — HTTP + stdio integration, tool dispatch.
- `pkg/mcpserver/panic_test.go` — handler panic isolation.
- `pkg/mcpserver/shutdown_test.go` — graceful shutdown with live SSE streams.
- `pkg/mcpserver/stdio_test.go` — Unix socket accept loop with backoff.
- `pkg/mcpserver/caller_test.go` — `HasCap` semantics.
