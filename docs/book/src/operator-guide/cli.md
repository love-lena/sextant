# CLI (`sextant`)

The operator CLI. Source: `cmd/sextant/`.

Top-level subcommands (`cmd/sextant/main.go`):

```
sextant init          # first-run setup
sextant doctor        # health diagnostics
sextant start         # detach sextantd as its own session leader
sextant stop          # SIGTERM the daemon, wait for graceful shutdown
sextant restart       # stop then start
sextant status        # daemon liveness + subprocess pids/addrs
sextant logs          # tail or follow the daemon log
sextant agents …      # agent lifecycle (7 subverbs: list|show|spawn|kill|restart|archive|prompt)
sextant ask           # synchronous prompt (publish + wait for turn_ended)
sextant conversation  # stream agent frames
sextant pending …     # user-input request queue (4 subverbs)
sextant files …       # read/list/tail files in a container (3 subverbs)
sextant exec          # run a command in a container
sextant audit …       # query / tail audit (2 subverbs)
sextant tail          # subscribe to an arbitrary NATS subject
sextant traces show   # render a distributed trace by trace_id
sextant worktree …    # manage worktrees (5 subverbs)
sextant templates …   # template management (currently: reload)
```

Every command supports `--json` for scriptable output. Exit codes (`cmd/sextant/main.go:105-108`):

- `0` — success.
- `1` — user error (bad args, agent not found).
- `2` — system error (daemon unreachable, RPC timeout, doctor failures).
- The container exec exit code is passed through verbatim by `sextant exec`.

`sextant <command> --help` prints per-command flags.

## `init`

```bash
sextant init [--config-dir DIR] [--data-dir DIR] [--force]
```

Generates `ca.{key,pub}`, writes `sextantd.toml` + `client.toml`, creates data dirs, seeds `default.toml` in the templates dir. Idempotent without `--force`.

## `doctor`

```bash
sextant doctor [--config-dir DIR] [--data-dir DIR] [--json]
```

Health probes: config files parse, CA keypair exists, sextantd reachable, NATS reachable, ClickHouse reachable, installed binary's `GitSHA` matches the daemon's. Exit `2` on any failure.

## Daemon lifecycle

Operator-facing wrappers around `sextantd` itself. All five share `--config-dir` / `--data-dir` flags that default to the canonical locations.

### `start`

```bash
sextant start [--config-dir DIR] [--data-dir DIR] [--timeout 30s]
```

Resolves the `sextantd` binary (in order: `$SEXTANTD_BIN`, sibling of the running `sextant` binary, then `$PATH`) and forks it as its own session leader. Stdout/stderr go to `<DataDir>/sextantd.log` (append, 0600). Waits up to `--timeout` for `runtime.json` to appear with a live PID; prints the last 50 log lines on timeout. Idempotent — if a live daemon already exists, exits 0 with an "already running" message. Stale `runtime.json` (file present, PID dead) is cleared automatically before the spawn.

### `stop`

```bash
sextant stop [--timeout 30s]
```

Sends `SIGTERM` to the PID in `runtime.json` and waits for the file to disappear (the daemon removes it during graceful shutdown). Never escalates to `SIGKILL` — that's an operator decision. Prints `daemon not running` and exits 0 when no daemon is recorded.

### `restart`

```bash
sextant restart [--stop-timeout 30s] [--start-timeout 30s]
```

Stop then start, with transition prints (`stopping daemon (pid N)`, `starting…`, `daemon up (pid M)`). Tolerates a not-running starting state.

### `status`

```bash
sextant status [--json]
```

Reads `runtime.json` and probes the PID with `signal 0`. Exit codes:

- `0` — alive (prints a table of daemon/nats/clickhouse/mcp pids + addrs).
- `1` — not running OR stale `runtime.json` (PID dead).

`--json` emits a structured row including `state` (`running` / `not_running` / `stale`) for scripts.

### `logs`

```bash
sextant logs [--follow] [--tail N]
```

Reads the daemon log (`<DataDir>/sextantd.log`). `--tail N` (default 50) prints the trailing N lines and exits unless `--follow` is set, in which case new bytes stream until Ctrl-C. Exit `1` if the log file does not exist.

## `agents`

| Verb                                                 | What it does                                                   |
|------------------------------------------------------|----------------------------------------------------------------|
| `agents list [--json]`                               | List every agent with UUID, name, template, lifecycle.         |
| `agents show <agent> [--json]`                       | Full status for one agent (UUID or name).                      |
| `agents spawn <name> --template <T> [--host <H>]`    | Create + start an agent. 60-second timeout.                    |
| `agents kill <agent> [--grace 10s] [--archive]`      | Stop the container. `--archive` also transitions to archived.  |
| `agents restart <agent> [--preserve-session]`        | Stop and respawn. `--preserve-session` is reserved (always on). |
| `agents archive <agent> [--all-dead]`                | Transition to archived. `--all-dead` bulk-archives `defined` agents. |
| `agents prompt <agent> "<text>"`                     | Send a prompt to the agent's inbox.                            |

`<agent>` accepts either the UUID or the name.

## `ask`

```bash
sextant ask <agent> "<text>" [--timeout 60s] [--json]
```

Synchronous prompt. Subscribes to the agent's `frames` and `lifecycle` subjects, then publishes the prompt via the `prompt_agent` RPC, then waits at the terminal until the agent emits `lifecycle.turn_ended` (or `lifecycle.ended`). Prints assistant frames inline as they arrive. Exits non-zero on timeout (default 60s).

The subscribe-before-publish ordering is load-bearing (`cmd/sextant/ask.go:79-86`) — it ensures the first frame isn't missed under JetStream's default `start-time=now` semantics.

Where to reach for this:

- Daily-drive operator workflow: ask a question, wait for the answer, see the result in your terminal.
- Replaces the three-command dance of starting `conversation --tail` in another terminal, then `agents prompt`, then `kill` once the reply lands.

There is **no MCP tool equivalent** of `ask` — agents that need to wait on another agent use `prompt_agent` plus their own subscription, or the `wait_for_agent_to_finish` tool described in the architecture spec (not yet implemented).

## `conversation`

```bash
sextant conversation <agent> [--tail] [--from-seq N] [--json]
```

Subscribes to `agents.<uuid>.frames` and `agents.<uuid>.lifecycle`. `--tail` exits when a `lifecycle.ended` envelope arrives. `--from-seq N` resumes from a specific JetStream sequence.

## `pending`

The user-input request queue (architecture §4a — wire-only, UX is TBD).

| Verb                                  | What it does                                              |
|---------------------------------------|-----------------------------------------------------------|
| `pending list [--since 1h] [--json]`  | Snapshot of unanswered requests (default lookback 1h; ~500ms quiet cutoff). |
| `pending answer <id> "<text>"`        | Publish a `UserInputResponse` with decision `answer`.     |
| `pending defer <id>`                  | Publish `decision=defer`.                                 |
| `pending escalate <id> --to <agent>`  | Publish `decision=escalate` to another agent.             |

## `files`

| Verb                                                  | What it does                                          |
|-------------------------------------------------------|-------------------------------------------------------|
| `files read <agent> <path> [--json]`                  | RPC `read_file`. 60-second timeout.                   |
| `files ls <agent> <path> [--json]`                    | RPC `list_dir`.                                       |
| `files tail <agent> <path> [--interval 500ms] [--json]` | Poll for new bytes (streaming RPC reserved).        |

## `exec`

```bash
sextant exec <agent> [--workdir /workspace] [--env K=V]... -- <cmd> [args...]
```

Runs a command in the agent's container (`exec_in_container` RPC, 5-minute timeout). stdout → stdout, stderr → stderr, exit code → exit. The `--` separator avoids confusing the flag parser with the command's flags.

## `audit`

| Verb                                                                          | What it does                                                  |
|-------------------------------------------------------------------------------|---------------------------------------------------------------|
| `audit query [--since 24h] [--actor X] [--action Y] [--agent Z] [--limit N] [--json]` | ClickHouse `audit` table query.                  |
| `audit tail [--filter ...] [--json]`                                          | Live subscribe to `audit.>`.                                  |

## `tail`

```bash
sextant tail <subject> [--from-seq N] [--json]
```

Subscribes to an arbitrary NATS subject. Wildcards allowed; bare `>` (the firehose) is refused — every subscription must bind one stream.

## `traces`

```bash
sextant traces show <trace_id> [--json]
```

Queries `telemetry_traces` for one trace, renders the spans as a tree (parent_span_id → children, then timestamp order).

## `worktree`

| Verb                                                            | What it does                                            |
|-----------------------------------------------------------------|---------------------------------------------------------|
| `worktree list [--json]`                                        | All registered worktrees.                               |
| `worktree create <name> [--base main] [--json]`                 | Create + check out a fresh branch.                      |
| `worktree destroy <name> [--force] [--json]`                    | Remove dir + registry entry.                            |
| `worktree merge <name> [--target main] [--json]`                | Merge under `locks.merge`.                              |
| `worktree diff <name> [--against main] [--json]`                | `git diff` output.                                      |
| `worktree prune [--apply] [--orphan-delete] [--json]`           | Enforce the 14d-archive / 30d-delete idle policy. Defaults to dry-run. |

Branch names must match `<kind>-<short-desc>-<seq>` per `conventions/git-workflow.md`. The pruner is documented in detail in [Worktrees](./worktrees.md) §"Pruning idle worktrees".

## `templates`

| Verb                                | What it does                                              |
|-------------------------------------|-----------------------------------------------------------|
| `templates reload [--json]`         | Re-scan `~/.config/sextant/templates/` and push to NATS KV. |

There is no `templates list` / `templates show` at this snapshot — read the files in `~/.config/sextant/templates/` directly.

## `version`, `help`

```bash
sextant version    # prints "sextant (M12)"
sextant help       # prints the top-level usage
```

**Note**: as of the snapshot's main, the dispatch table has 13 verbs (the 12 above plus `ask`). The `version` string `"sextant (M12)"` is a hand-rolled label, not a generated build version.

## Not yet implemented

The CLI spec at `specs/cli/commands.md` mentions `sextant self` (M16 self-update) and `sextant test` (M17 test environments). Neither exists at this snapshot.
