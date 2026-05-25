# RPC dispatch

**Source**: `pkg/rpc/`, `pkg/rpc/handlers/`.

`pkg/rpc` is the server side of sextant's request/reply RPC. It subscribes to `sextant.rpc.*`, dispatches by verb, applies capability checks, runs the typed handler, audits, and publishes the response on the caller's reply subject.

## When to reach for this component

- You're adding a new RPC verb.
- You're changing capability gating or audit behaviour for an existing verb.
- You're investigating a `not_implemented` or `internal` error.

## Public surface

| Symbol                            | File                                                  | Purpose                                                       |
|-----------------------------------|-------------------------------------------------------|---------------------------------------------------------------|
| `Server`                          | `pkg/rpc/server.go`                                   | The dispatcher. Constructs handlers; subscribes to `sextant.rpc.*`. |
| `CapFor(verb)`                    | `pkg/rpc/types.go` (lines 8-35)                       | Verb → capability string.                                     |
| Verb constants                    | `pkg/rpc/types.go:38-64`                              | One `Verb*` constant per implemented verb.                    |
| Handler constructors              | `pkg/rpc/handlers/*.go`                               | One `New<Verb>(deps)` per verb.                               |

## Verb catalog

19 verbs at this snapshot. Each maps to one file in `pkg/rpc/handlers/`:

| Verb                   | Capability               | Handler file               |
|------------------------|--------------------------|----------------------------|
| `list_agents`          | `read.agents`            | `handlers/agents.go:36`    |
| `get_agent_status`     | `read.agents`            | `handlers/agents.go:94`    |
| `read_file`            | `read.container_files`   | `handlers/files.go`        |
| `list_dir`             | `read.container_files`   | `handlers/files.go`        |
| `stat`                 | `read.container_files`   | `handlers/files.go`        |
| `exec_in_container`    | `control.exec`           | `handlers/files.go`        |
| `query_history`        | `read.history`           | `handlers/query_history.go:27` |
| `query_audit`          | `read.history`           | `handlers/query_audit.go:24`   |
| `query_trace`          | `read.history`           | `handlers/query_trace.go:22`   |
| `spawn_agent`          | `control.spawn`          | `handlers/spawn.go`        |
| `kill_agent`           | `control.kill`           | `handlers/kill.go:38`      |
| `archive_agent`        | `control.archive`        | `handlers/archive.go:53`   |
| `prompt_agent`         | `control.prompt`         | `handlers/prompt.go:49`    |
| `restart_agent`        | `control.restart`        | `handlers/restart.go:76`   |
| `worktree_create`      | `control.worktree`       | `handlers/worktree.go:36`  |
| `worktree_destroy`     | `control.worktree`       | `handlers/worktree.go:60`  |
| `worktree_list`        | `read.worktrees`         | `handlers/worktree.go:78`  |
| `worktree_merge`       | `control.worktree`       | `handlers/worktree.go`     |
| `worktree_diff`        | `read.worktrees`         | `handlers/worktree.go`     |

The full [RPC catalog](../protocols/rpc-catalog.md) chapter has request/response shapes for each.

## Dispatch flow

1. The server subscribes to `sextant.rpc.*`. NATS routes any `sextant.rpc.<verb>` message to it.
2. Decode the envelope; assert `Kind == "rpc_request"` and that the inner `RPCRequest.Verb` is registered.
3. Resolve the caller. For the operator path (Unix-socket-or-equivalent), `actor=operator`. The architecture spec (§10b) reserves real-JWT operator auth for the multi-user v2.
4. Capability check via `CapFor(verb)`. Operator bypasses; agents must carry the capability in their JWT.
5. Look up the cached idempotency response. If the same `(verb, idempotency_key)` arrived within the cache window (~60s), return the cached bytes.
6. Invoke the typed handler. Panics are recovered and converted to `internal`.
7. Publish an audit envelope (`audit.<verb>` or `audit.access`) including the result.
8. Publish the `RPCResponse` envelope to `ReplyTo`.

## Adding a new verb

1. Define request/response structs in `pkg/sextantproto/rpcverbs.go`. Add the verb name as a constant in `pkg/rpc/types.go`. Add the capability mapping in `CapFor`.
2. Create `pkg/rpc/handlers/<verb>.go` with a `New<Verb>(deps)` constructor. The handler signature is shared across the package; look at an existing one for the pattern.
3. Register the handler in `pkg/rpc/server.go` so the dispatcher knows about it.
4. Add tests in `pkg/rpc/handlers/<verb>_test.go`. The package has a fake-NATS / fake-KV setup; most existing tests show the pattern.
5. Run `go generate ./pkg/sextantproto/...` to refresh the JSON schemas. Then `cd clients/typescript && npm run codegen` to refresh the TS types.

## What's *not* a verb

A few items from `specs/protocols/rpc-catalog.md` and `specs/protocols/bus-subjects.md` are listed but not implemented at this snapshot:

- `read_file_stream` — `read_file` exists; streaming is not.
- `trigger_thought_dump`, `enable_verbose_logging` — debug telemetry RPCs; not implemented.
- `get_session_summary` — not implemented; the data is reachable via `query_history`.
- `self_update`, `self_rollback`, `query_deploy_history` — M16.
- `provision_test_environment`, `teardown_test_environment`, `list_test_environments`, `connect_to_test_environment` — M17.

See [Known gaps and drift](../reference/known-gaps.md) for the full list.
