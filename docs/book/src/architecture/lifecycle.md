# Agent lifecycle

Agents have two state machines: the *definition* lifecycle (durable record) and the *incarnation* state (live process).

## Definition lifecycle

Field: `AgentDefinition.Lifecycle` (`pkg/sextantproto/agent.go:71`; `LifecycleState` is declared at line 8). Values:

```
defined ──spawn──▶ running ──kill──▶ defined ──archive──▶ archived
                              │
                              └──pause──▶ paused (not yet implemented)
```

| State      | Meaning                                                                  |
|------------|--------------------------------------------------------------------------|
| `defined`  | Record exists in `agent_definitions`. No incarnation. Name reserved.     |
| `running`  | One incarnation alive (`agent_incarnations` has a row in state `ready`). |
| `paused`   | Reserved. The wire constant exists; no handler transitions into it yet.  |
| `archived` | Definition kept for history. Name released for reuse.                    |

Transitions:

- `defined → running`: `sextant agents spawn` (or `spawn_agent` MCP tool).
- `running → defined`: `sextant agents kill` — stops the container, leaves the definition.
- `defined/running → archived`: `sextant agents archive` (or `archive_agent` MCP tool). If the agent is running, the handler kills it first.

Each definition mutation records an event in the `audit` stream (`audit.spawn`, `audit.kill`, `audit.definition_change`, etc.) and writes a row to `agent_definitions_history` in ClickHouse via the shipper.

## Incarnation state

Field: `AgentIncarnation.State` (`pkg/sextantproto/agent.go:92`; `IncarnationState` is declared at line 98). Values:

```
starting ──container ready──▶ ready ──container exit──▶ exited
                                            │
                                            └─crash──▶ failed
```

| State      | Meaning                                                                            |
|------------|------------------------------------------------------------------------------------|
| `starting` | Container created, sidecar not yet on the bus.                                     |
| `ready`    | Sidecar published `lifecycle.started`, listening on inbox.                         |
| `exited`   | Clean SIGTERM shutdown. The sidecar published `lifecycle.ended`.                   |
| `failed`   | Container died unexpectedly. Heartbeat or container-inspect signaled the failure.  |

Only one incarnation is alive per agent UUID at a time. Restart cycles bump the `agent_incarnations` row (or create a new one — see `pkg/rpc/handlers/restart.go`).

## Spawn sequence — end-to-end

1. Operator runs `sextant agents spawn <name> --template <T>` (`cmd/sextant/agents.go:258`).
2. CLI sends a `spawn_agent` RPC over NATS (`sextant.rpc.spawn_agent`).
3. Handler at `pkg/rpc/handlers/spawn.go` runs:
   - Validates the agent name doesn't collide with another living agent.
   - Loads the template from the `templates` KV bucket.
   - Generates a fresh UUID and IncarnationID.
   - Issues a JWT with the template's `permissions` array, signed by the CA (24h lifetime).
   - Allocates a worktree if `mounts` includes `worktree` (see `pkg/worktree.SpawnWorktreeName`).
   - If `claude_seed` is set, ensures the per-agent named volume and populates it from the host seed dir (copy-on-spawn).
   - Builds the container spec: image, env, mounts, labels, resource limits.
   - Calls `pkg/containermgr.Run`, which calls `ContainerCreate` then `ContainerStart` on the Docker daemon.
   - Persists `AgentDefinition` (lifecycle=running) and `AgentIncarnation` (state=starting) to KV.
   - Publishes `audit.spawn`.
4. Inside the container, `entrypoint.sh` execs `node /opt/sextant/sidecar/dist/index.js run`.
5. The sidecar (`images/sidecar/entrypoint/src/index.ts:879-1003`):
   - Connects to NATS using the operator user creds passed via env (M11 transitional path).
   - Optionally connects to the sextant MCP server using the JWT.
   - Publishes `lifecycle.started` with `state=running`.
   - Subscribes to `agents.<uuid>.inbox` with `deliverAll: true`.
   - Starts a heartbeat ticker (5s interval) on `agents.<uuid>.heartbeat`.
   - Waits for prompts; each prompt invokes the Claude Agent SDK in a serial queue.

## Prompt sequence

1. Operator runs `sextant agents prompt <agent> "<content>"`.
2. The handler at `pkg/rpc/handlers/prompt.go` publishes a `KIND_AGENT_FRAME` envelope on `agents.<uuid>.inbox` (or via MCP `prompt_agent` for agent callers).
3. The sidecar's inbox subscriber pushes the prompt onto its `PromptQueue` (`images/sidecar/entrypoint/src/index.ts:773-818`).
4. The queue invokes `driver.runTurn(prompt)`. With `--driver=sdk` (the default), this calls `query()` on `@anthropic-ai/claude-agent-sdk` with the configured model, system prompt, and `mcpServers.sextant` block.
5. The SDK streams messages back. Each is translated to an `agent_frame` envelope (`frame_kind`: `assistant_text`, `tool_call`, `tool_result`, `system_note`, `error`) and published on `agents.<uuid>.frames`.
6. When the SDK loop completes, the sidecar publishes a `lifecycle.turn_ended` transition (with `reason=error` if applicable) and persists the SDK's `session_id` to `agent_definitions.<uuid>.runtime.session_id` via CAS so the next incarnation can resume.

## Restart and resume

`sextant agents restart <agent>` (`pkg/rpc/handlers/restart.go:76`):

1. Stops the current container with grace.
2. Issues a fresh JWT (new IncarnationID).
3. If `--preserve-session` was passed AND the agent definition has a recorded `session_id`, forwards `SEXTANT_SESSION_ID=<saved>` into the new container (`pkg/rpc/handlers/restart.go:167-170`). Without the flag, the env var is omitted and the SDK starts a fresh session.
4. Spawns the new container.
5. The sidecar passes the session id to the SDK as `options.resume`, restoring the conversation history; if the env var is absent, the next turn writes a new session id back to the definition via CAS.

In short: `--preserve-session` is the operator's choice. The default behaviour (`--preserve-session=false`) drops the existing session id.

## Shutdown

The sidecar handles `SIGTERM` and `SIGINT` (`images/sidecar/entrypoint/src/index.ts:997-1002`):

1. Stop accepting new prompts.
2. Wait up to 5s for the in-flight turn to settle.
3. Wait up to 2s for any in-flight heartbeat tick.
4. Publish `lifecycle.ended` with `reason=signal:SIGTERM`.
5. Close the MCP client (if connected) and the NATS connection.
6. `process.exit(0)`.

When `sextantd` itself shuts down, it stops every supervised subprocess in reverse startup order: agent containers (via `containermgr.Stop`), then MCP and RPC servers, then the shipper, then ClickHouse, then NATS. See [sextantd](../components/sextantd.md) for the daemon-side sequence.
