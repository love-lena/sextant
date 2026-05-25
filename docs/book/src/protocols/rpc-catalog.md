# RPC catalog

The implemented RPC verbs at this snapshot. Source: `pkg/rpc/types.go:38-64` for the constants, `pkg/rpc/handlers/*.go` for the behaviour, `pkg/sextantproto/rpcverbs.go` for the request/response struct shapes.

All RPCs go through `sextant.rpc.<verb>`. All return either a typed result or an `RPCError{Code, Message, Details}`.

Error codes (`pkg/sextantproto/rpc.go:34-49`):

```
unknown_verb
capability_denied
agent_not_found
timeout
bad_request
internal
stream_canceled
idempotency_replay
not_implemented
not_found
```

## Read — agents

### `list_agents`
- Cap: `read.agents`
- Request: `ListAgentsRequest{ Filter?: ListAgentsFilter }`
- Response: `ListAgentsResponse{ Agents: []AgentSummary }` (`pkg/sextantproto/rpcverbs.go:35`)
- Handler: `pkg/rpc/handlers/agents.go:36`

Optional filter narrows by lifecycle (`defined`, `running`, `paused`, `archived`).

### `get_agent_status`
- Cap: `read.agents`
- Request: `GetAgentStatusRequest{ AgentID: string }`
- Response: `GetAgentStatusResponse{ Status: ... }`
- Handler: `pkg/rpc/handlers/agents.go:94`

`AgentID` accepts either a UUID or a name. Unknown ID returns `agent_not_found` (HTTP-style 404).

## Read — container files

### `read_file`
- Cap: `read.container_files`
- Request: `ReadFileRequest{ AgentID, Path }`
- Response: `ReadFileResponse{ Content, ContentType }`
- Handler: `pkg/rpc/handlers/files.go`

Runs against the agent's `/workspace` (or other mounts) via Docker exec. Non-streaming — large files come back in one response.

### `list_dir`
- Cap: `read.container_files`
- Request: `ListDirRequest{ AgentID, Path }`
- Response: `ListDirResponse{ Entries: []ListDirEntry }`
- Handler: `pkg/rpc/handlers/files.go`

### `stat`
- Cap: `read.container_files`
- Request: `StatRequest{ AgentID, Path }`
- Response: `StatResponse{ Name, Size, Mode, IsDir }`
- Handler: `pkg/rpc/handlers/files.go`

## Read — history (ClickHouse-backed)

### `query_history`
- Cap: `read.history`
- Request: `QueryHistoryRequest{ Filter: QueryHistoryFilter, TimeRange?: QueryHistoryTimeRange, Limit? }`
- Response: `QueryHistoryResponse{ Events: []Envelope }`
- Handler: `pkg/rpc/handlers/query_history.go:27`

`QueryHistoryFilter` accepts subject patterns and kind allowlists. Results come from ClickHouse `events`.

### `query_audit`
- Cap: `read.history`
- Request: `QueryAuditRequest{ Filter: QueryAuditFilter, TimeRange?, Limit? }`
- Response: `QueryAuditResponse{ Rows: []QueryAuditRow }`
- Handler: `pkg/rpc/handlers/query_audit.go:24`

`QueryAuditFilter` narrows by `actor`, `action`, and `agent_uuid`. Results come from ClickHouse `audit`.

### `query_trace`
- Cap: `read.history`
- Request: `QueryTraceRequest{ TraceID: string }`
- Response: `QueryTraceResponse{ Spans: []QueryTraceSpan }`
- Handler: `pkg/rpc/handlers/query_trace.go:22`

Walks ClickHouse `telemetry_traces` for one trace ID, returning all spans for the operator's renderer to assemble (`sextant traces show`).

## Control — agent lifecycle

### `spawn_agent`
- Cap: `control.spawn`
- Request: `SpawnAgentRequest{ Name, Template, HostPin?, DefinitionOverrides? }`
- Response: `SpawnAgentResponse{ AgentID: string }`
- Handler: `pkg/rpc/handlers/spawn.go`

Loads the template, creates definition + incarnation in KV, issues a JWT, populates any `claude_seed` volume, and starts the Docker container. See [Agent lifecycle](../architecture/lifecycle.md).

### `kill_agent`
- Cap: `control.kill`
- Request: `KillAgentRequest{ AgentID, GraceSeconds? }`
- Response: `KillAgentResponse{ OK: bool }`
- Handler: `pkg/rpc/handlers/kill.go:38`

Stops the running container with grace; transitions definition to `defined` (keeps the record).

### `archive_agent`
- Cap: `control.archive`
- Request: `ArchiveAgentRequest{ AgentID, GraceSeconds? }`
- Response: `ArchiveAgentResponse{ OK: bool }`
- Handler: `pkg/rpc/handlers/archive.go:53`

Kills the agent if running, then transitions to `archived` and releases the name. Removes the per-agent `.claude` named volume.

### `restart_agent`
- Cap: `control.restart`
- Request: `RestartAgentRequest{ AgentID, PreserveSession?: bool }`
- Response: `RestartAgentResponse{ AgentID, OK }`
- Handler: `pkg/rpc/handlers/restart.go:76`

Kills the container, then spawns a new one. `PreserveSession=true` forwards `SEXTANT_SESSION_ID=<saved>` (when a `session_id` is recorded on the definition) so the SDK resumes the conversation; `PreserveSession=false` (default) drops the session and the SDK starts fresh.

### `prompt_agent`
- Cap: `control.prompt`
- Request: `PromptAgentRequest{ AgentID, Content }`
- Response: `PromptAgentResponse{ OK }`
- Handler: `pkg/rpc/handlers/prompt.go:49`

Publishes a `KIND_AGENT_FRAME` envelope on `agents.<uuid>.inbox`.

### `exec_in_container`
- Cap: `control.exec`
- Request: `ExecInContainerRequest{ AgentID, Cmd: []string, Workdir?, Env? }` (JSON key `workdir`)
- Response: `ExecInContainerResponse{ Stdout, Stderr, ExitCode }`
- Handler: `pkg/rpc/handlers/files.go`

Mirrors `docker exec`: stdout, stderr, exit code captured and returned. Used by `sextant exec`.

## Control — worktrees

### `worktree_create`
- Cap: `control.worktree`
- Request: `WorktreeCreateRequest{ Name, BaseBranch? }`
- Response: `WorktreeCreateResponse{ Worktree: WorktreeInfo }`
- Handler: `pkg/rpc/handlers/worktree.go:36`

### `worktree_destroy`
- Cap: `control.worktree`
- Request: `WorktreeDestroyRequest{ Name, Force?: bool }`
- Response: `WorktreeDestroyResponse{ OK }`
- Handler: `pkg/rpc/handlers/worktree.go:60`

### `worktree_list`
- Cap: `read.worktrees`
- Request: `WorktreeListRequest{}`
- Response: `WorktreeListResponse{ Worktrees: []WorktreeInfo }`
- Handler: `pkg/rpc/handlers/worktree.go:78`

### `worktree_merge`
- Cap: `control.worktree`
- Request: `WorktreeMergeRequest{ Name, Target? }`
- Response: `WorktreeMergeResponse{ OK, Branch, Target, Conflicts: []string }`
- Handler: `pkg/rpc/handlers/worktree.go`

`OK=false` with non-empty `Conflicts` is the conflict result; the merge has been aborted on the daemon side.

### `worktree_diff`
- Cap: `read.worktrees`
- Request: `WorktreeDiffRequest{ Name, Against? }`
- Response: `WorktreeDiffResponse{ Diff: string }`
- Handler: `pkg/rpc/handlers/worktree.go`

## Idempotency

Every verb supports an `IdempotencyKey` on the envelope. The dispatcher caches `(verb, idempotency_key) → response_bytes` for ~60 seconds and returns the cached bytes for a repeat call. This protects control-plane operations from accidental double-execution caused by retries.

## Capability table

| Capability                | Verbs                                                                |
|---------------------------|----------------------------------------------------------------------|
| `read.agents`             | `list_agents`, `get_agent_status`                                    |
| `read.history`            | `query_history`, `query_audit`, `query_trace`                        |
| `read.container_files`    | `read_file`, `list_dir`, `stat`                                      |
| `read.worktrees`          | `worktree_list`, `worktree_diff`                                     |
| `control.exec`            | `exec_in_container`                                                  |
| `control.spawn`           | `spawn_agent`                                                        |
| `control.kill`            | `kill_agent`                                                         |
| `control.archive`         | `archive_agent`                                                      |
| `control.restart`         | `restart_agent`                                                      |
| `control.prompt`          | `prompt_agent`                                                       |
| `control.worktree`        | `worktree_create`, `worktree_destroy`, `worktree_merge`              |

For MCP tools (a slightly different surface), see [mcpserver](../components/mcpserver.md).
