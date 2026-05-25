# CLI (`sextant`)

The operator CLI. Source: `cmd/sextant/`.

Twelve top-level subcommands (`cmd/sextant/main.go:46-69`):

```
sextant init          # first-run setup
sextant doctor        # health diagnostics
sextant agents …      # agent lifecycle (7 subverbs: list|show|spawn|kill|restart|archive|prompt)
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

Branch names must match `<kind>-<short-desc>-<seq>` per `conventions/git-workflow.md`.

## `templates`

| Verb                                | What it does                                              |
|-------------------------------------|-----------------------------------------------------------|
| `templates reload [--json]`         | Re-scan `~/.config/sextant/templates/` and push to NATS KV. |

There is no `templates list` / `templates show` at this snapshot — read the files in `~/.config/sextant/templates/` directly.

## `version`, `help`

```bash
sextant version    # prints "sextant initial (M12)"
sextant help       # prints the top-level usage
```

## Not yet implemented

The CLI spec at `specs/cli/commands.md` mentions `sextant self` (M16 self-update) and `sextant test` (M17 test environments). Neither exists at this snapshot.
