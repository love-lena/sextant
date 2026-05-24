# CLI commands — spec

The `sextant` binary (`cmd/sextant/`) is the operator's primary interaction surface during phase 1 and the foundation for everything during phase 2.

Built on `sextant-client-go`. Every command supports `--json` for scriptable output.

## Top-level structure

`sextant <noun> <verb> [args...]` is the canonical shape.

```
sextant init                          # first-run setup
sextant agents <verb>                 # agent operations
sextant conversation <agent>          # conversation tail
sextant pending                       # user-input request queue
sextant files <verb>                  # container filesystem access
sextant exec <agent> -- <cmd>         # exec in container
sextant audit <verb>                  # audit log
sextant traces <verb>                 # OTel traces
sextant worktree <verb>               # worktree management (post-M14)
sextant self <verb>                   # self-management (post-M16)
sextant test <verb>                   # test envs (post-M17)
sextant doctor                        # health diagnostics
```

## Commands

### `sextant init`

First-run setup. Idempotent — re-running detects existing state and skips.

- Generates signing CA keypair at `~/.config/sextant/ca.key` + `~/.config/sextant/ca.pub`
- Writes `~/.config/sextant/sextantd.toml` with sensible defaults
- Writes `~/.config/sextant/client.toml` with NATS connection path
- Creates data dirs: `~/.local/share/sextant/{nats,clickhouse,shipper-buffer,test}`
- Pulls or builds `sextant-sidecar:latest` image
- Creates initial templates at `~/.config/sextant/templates/`; format defined in `specs/architecture.md` §11b (Templates). Default template `default.toml` ships with init.
- Starts sextantd and waits for ready

### `sextant agents <verb>`

| Verb | Purpose | RPC |
|---|---|---|
| `list` | List agents | `list_agents` |
| `show <agent>` | Detailed status | `get_agent_status` |
| `spawn <name> --template T [--host H]` | Create + start | `spawn_agent` |
| `kill <agent> [--grace 10s]` | Terminate | `kill_agent` |
| `restart <agent> [--preserve-session]` | Restart | `restart_agent` |
| `prompt <agent> "<text>"` | Send a prompt | `prompt_agent` |
| `archive <agent>` | Move to archived state | `archive_agent` |

### `sextant conversation <agent> [--tail] [--from-seq N]`

Subscribe to `agents.<uuid>.frames` and print frames in human-readable form. `--tail` exits on session-end lifecycle; otherwise streams forever.

### `sextant pending`

Lists user-input requests across all agents. Sub-verbs:

- `sextant pending list` — show queue
- `sextant pending answer <request_id> "<answer>"` — answer one
- `sextant pending defer <request_id>` — defer to operator
- `sextant pending escalate <request_id> --to <agent>` — escalate

### `sextant files <verb>`

| Verb | Purpose | RPC |
|---|---|---|
| `read <agent> <path>` | Read file from container | `read_file` |
| `ls <agent> <path>` | List directory | `list_dir` |
| `tail <agent> <path>` | tail -f over RPC | `read_file_stream` |

### `sextant exec <agent> -- <cmd> [args...]`

Run command inside agent's container. Capability-gated (operator-level). Audited.

### `sextant audit <verb>`

| Verb | Purpose |
|---|---|
| `query [--since 1h] [--agent X] [--action spawn]` | Filter audit log |
| `tail [--filter ...]` | Live audit stream |

### `sextant traces show <trace_id>`

Display a full distributed trace via spans from ClickHouse.

### `sextant worktree <verb>` (post-M14)

| Verb | Purpose |
|---|---|
| `list` | Show all worktrees |
| `create <name> [--base main]` | Create new worktree |
| `destroy <name>` | Clean up worktree |
| `merge <name> [--target main]` | Merge to target |
| `diff <name> [--against main]` | Show diff |

### `sextant self <verb>` (post-M16)

| Verb | Purpose |
|---|---|
| `up [--target <revision>]` | Build + deploy new version |
| `rollback` | Roll back to previous |
| `status` | Current revision + deploy history |

### `sextant test <verb>` (post-M17)

| Verb | Purpose |
|---|---|
| `provision [--ttl 60m] [--profile default]` | Create test env |
| `list` | Active test envs |
| `teardown <test_id>` | Destroy test env |
| `connect <test_id>` | Print connection details for shell use |

### `sextant doctor`

Component health diagnostics — reports status of NATS, ClickHouse, sextantd, shipper, plus config paths, CA key location, recent error counts.

## Output formats

- Default: human-readable, paginated through `less -FX` when interactive
- `--json`: machine-parseable JSON to stdout
- `--quiet`: just the result, no chrome
- `--verbose`: include debug info

## Exit codes

- `0` — success
- `1` — user error (bad args, agent not found, etc.)
- `2` — system error (daemon unreachable, RPC timeout, etc.)

## Open

- Tab-completion shipping — generate per-shell completions on `sextant init`?
- Config override via env vars — `SEXTANT_NATS_URL=...` etc. — lean yes
- Plugin commands — third-party CLI verbs via `~/.config/sextant/plugins/`? Probably v2.
