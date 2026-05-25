# Architecture overview

Sextant initial is built around four load-bearing properties. If you understand these, the rest of the system follows.

## 1. Everything on the bus

NATS JetStream is the IPC for every component crossing a process boundary. There is no separate gRPC/HTTP control plane. There are two patterns on the bus:

- **Pub/sub on subjects** for events (agent frames, lifecycle, audit, telemetry, heartbeats).
- **Request/reply on RPC subjects** for synchronous "do this now" or "fetch that now" calls (`sextant.rpc.<verb>`).

The single NATS substrate carries both. See [Bus subjects](../protocols/bus-subjects.md) for the subject map and [RPC catalog](../protocols/rpc-catalog.md) for the verbs.

## 2. Three-layer data architecture

| Layer            | What lives there                                                | Retention           |
|------------------|-----------------------------------------------------------------|---------------------|
| **NATS streams** | Event log, working window for gap-fill + live replay            | hours to ~365 days  |
| **NATS KV**      | Current state — agent definitions, incarnations, templates, locks, ui state | latest value only |
| **ClickHouse**   | Long-term queryable store                                       | indefinite          |

`sextant-shipper` subscribes to NATS streams and writes rows to ClickHouse with at-least-once delivery (de-duped on primary key). See [shipper](../components/shipper.md).

Streams created at NATS bootstrap (`pkg/natsboot/layout.go:Streams()`):

| Stream             | Subject pattern         | Max age   |
|--------------------|-------------------------|-----------|
| `agent_frames`     | `agents.*.frames`       | 7 days    |
| `agent_lifecycle`  | `agents.*.lifecycle`    | 30 days   |
| `agent_heartbeats` | `agents.*.heartbeat`    | 1 hour    |
| `agent_inbox`      | `agents.*.inbox`        | 24 hours  |
| `audit`            | `audit.>`               | 365 days  |
| `telemetry_traces` | `telemetry.traces.>`    | 7 days    |
| `telemetry_metrics`| `telemetry.metrics.>`   | 30 days   |
| `telemetry_logs`   | `telemetry.logs.>`      | 7 days    |
| `user_input`       | `user_input.>`          | 30 days   |
| `control_rpc`      | `sextant.rpc.>`         | 24 hours  |
| `system`           | `sextant.system.>`      | 30 days   |

KV buckets created at NATS bootstrap:

- `agent_definitions`, `agent_incarnations` — durable + live state
- `templates` — synced from `~/.config/sextant/templates/`
- `worktrees` — the worktree registry
- `locks` — merge and deploy locks
- `ui_state` — per-operator UI coordination
- `viz_specs`, `test_envs` — reserved (the latter is M17, not yet active)

## 3. Container per agent incarnation

Each running agent has exactly one container. The container is the isolation boundary. Policy inside is permissive by default (broad mounts, open egress); the container is the wall.

The container image — `sextant-sidecar:<tag>` — is Debian-based and pre-built (`make sidecar-image`). It bakes in the Claude Agent SDK, the `@sextant/client` library, and a rich common-tools set (git, gh, jq, ripgrep, fzf, curl, build tools, Go, Node, Python — `images/sidecar/Dockerfile`).

The container's filesystem is augmented at spawn time (`pkg/containermgr` + spawn handlers in `pkg/rpc/handlers/spawn.go`):

| Mount path                                  | Backing storage                                           | Mode |
|---------------------------------------------|-----------------------------------------------------------|------|
| `/workspace`                                | The agent's git worktree on the host                      | rw   |
| `/workspace/.git` (worktree gitdir)         | Host's worktree-administrative `.git/worktrees/<name>` dir | rw   |
| `/home/agent/.gitconfig`                    | Host bind, read-only                                       | ro   |
| `/home/agent/.ssh`                          | Operator's `~/.ssh` (only when template `mounts` includes `"ssh"`) | ro   |
| `/home/agent/.claude`                       | Per-agent Docker named volume (when `claude_seed_mode = "copy-on-spawn"`) or host bind-ro (when `"readonly-bind"`); omitted when no `claude_seed` set | rw / ro |

> **Scope note**: the architecture spec (§3) lists additional mount classes (`/home/agent/.{cargo,npm,cache,local/share}` as per-agent named volumes, `/run/sextant/secrets/` from a template's `secrets` mount class). These are **not implemented at this snapshot** — the spawn handler only wires the mounts listed above. The `mounts = [...]` template field is honoured for `worktree` (resolves to `/workspace`) and `ssh` (resolves to `/home/agent/.ssh`); the `secrets` class is allowlisted in `pkg/templates/template.go:KnownMountClasses()` but not yet wired by the spawn handler.

Plus container env vars set by `buildContainerEnv` (`pkg/rpc/handlers/container_env.go`):

| Variable                  | Required? |
|---------------------------|-----------|
| `SEXTANT_AGENT_UUID`      | yes       |
| `SEXTANT_AGENT_NAME`      | yes       |
| `SEXTANT_INCARNATION_ID`  | yes       |
| `SEXTANT_HOST_ID`         | yes       |
| `SEXTANT_NATS_URL`        | yes       |
| `SEXTANT_NATS_USER`       | yes       |
| `SEXTANT_NATS_PASSWORD`   | yes       |
| `SEXTANT_JWT`             | optional (consumed by MCP only) |
| `SEXTANT_MCP_URL`         | optional  |
| `SEXTANT_SESSION_ID`      | optional (set on restart with `--preserve-session`) |
| `SEXTANT_MODEL`           | optional  |
| `SEXTANT_PERMISSION_MODE` | optional  |
| `SEXTANT_INITIAL_PROMPT`  | optional (base64) |
| `ANTHROPIC_API_KEY`       | forwarded from host |

See [Sidecar image](../components/sidecar-image.md) §"Container environment variables".

## 4. Definitions vs incarnations

Agents are *records*. Processes are *incarnations*. A definition can exist with no incarnation (`lifecycle = defined`), be running with exactly one incarnation (`lifecycle = running`), or be archived (`lifecycle = archived`, name released for reuse).

Identity has two parts (`pkg/sextantproto/agent.go`):

- `AgentDefinition.UUID` — permanent. Bus messages reference UUIDs internally.
- `AgentDefinition.Name` — human-readable. Unique among non-archived agents. Mutable.

Per-incarnation identity:

- `AgentIncarnation.IncarnationID` — unique per spawn. Embedded in every JWT.
- `AgentIncarnation.ContainerID` — Docker container long ID.
- `AgentIncarnation.State` — `starting`, `ready`, `exited`, or `failed`.

See [Agent lifecycle](./lifecycle.md) for the state machine.

## Process model summary

| Process                | One per                  | Lives where                  |
|------------------------|--------------------------|------------------------------|
| `sextantd`             | install                  | host                         |
| `nats-server`          | install (supervised)     | host, child of `sextantd`    |
| `clickhouse-server`    | install (supervised)     | host, child of `sextantd`    |
| `sextant-shipper`      | install (supervised, optional) | host, child of `sextantd` |
| Agent sidecar          | running agent incarnation | container, child of dockerd |
| MCP server             | install                  | in-process inside `sextantd` |
| RPC server             | install                  | in-process inside `sextantd` |

See [Process model](./process-model.md) for the start/stop choreography.

## What's *not* in this build

The architecture spec describes a few headline pillars that are not yet wired into code at this snapshot:

- **§7 multi-host federation** — partially. Worker certs / CA signing for cross-host is designed but not exercised; the default deployment is all components on one host.
- **§4a user-input propagation pattern** — RPC + tools exist; the layered review / batching UX is not built.
- **§12 self-update** — `SIGUSR2` is wired to a log-only stub; the staging worktree, watchdog, and rollback are M16.
- **§13 ephemeral test environments** — entirely M17 territory.
- **Per-agent tool / package-manager volumes** (`.cargo`, `.npm`, `.cache`, `.local/share`) — described in `architecture.md` §3. Not wired by the current spawn handler.
- **`secrets` mount class** — described in `architecture.md` §3 and §11b. Not implemented; the `~/.config/sextant/secrets/` directory has no code path that reads it.

These are flagged inline in the relevant component chapters and consolidated in [Known gaps and drift](../reference/known-gaps.md).
