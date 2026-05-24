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

## Open

- Per-verb detailed schemas (request/response struct fields) — keep here until they grow large enough to warrant own files
- Streaming response semantics — ephemeral subject is the lean; final detail TBD during M7 implementation
- Capability grant/revoke at runtime — out of scope for initial; JWTs are immutable per incarnation
