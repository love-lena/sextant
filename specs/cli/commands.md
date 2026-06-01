# CLI commands — spec

The `sextant` binary (`cmd/sextant/`) is the operator's primary interaction surface during phase 1 and the foundation for everything during phase 2.

Built on `sextant-client-go`. Every command supports `--json` for scriptable output.

## Top-level structure

`sextant <noun> <verb> [args...]` is the canonical shape.

```
sextant init                          # first-run setup (top-level singleton)
sextant doctor                        # health diagnostics (top-level singleton)
sextant version                       # print version (top-level singleton)

sextant daemon <verb>                 # daemon lifecycle (start|stop|restart|status|logs)
sextant agents <verb>                 # agent ops (incl. chat, exec — folded under agents)
sextant pending <verb>                # user-input request queue
sextant files <verb>                  # container filesystem access
sextant audit <verb>                  # audit log
sextant events <verb>                 # subscribe to any bus subject (events tail)
sextant traces <verb>                 # OTel traces
sextant worktree <verb>               # worktree management
sextant templates <verb>              # template reload
sextant self <verb>                   # self-management (post-M16)
sextant test <verb>                   # test envs (post-M17)
```

### Resource-verb migration (May 2026)

Per `slug:feat-cli-resource-verb-cleanup`, the following
top-level verbs moved under resource nouns. Old forms remain as
deprecated aliases for one minor release:

| Old | New |
|---|---|
| `sextant ask <agent> "<text>"` | `sextant agents chat <agent> "<text>"` (one-shot mode) |
| `sextant conversation <agent>` | `sextant agents chat <agent>` (TUI mode) |
| `sextant exec <agent> -- <cmd>` | `sextant agents exec <agent> -- <cmd>` |
| `sextant tail <subject>` | `sextant events tail <subject>` |
| `sextant start` | `sextant daemon start` |
| `sextant stop` | `sextant daemon stop` |
| `sextant restart` | `sextant daemon restart` |
| `sextant status` | `sextant daemon status` |
| `sextant logs` | `sextant daemon logs` |

`init`, `doctor`, `version` remain top-level singletons (verbs on the
sextant install itself — explicit exceptions per
`conventions/tui-conventions.md`).

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
| `create <name> --template T [--host H]` | Create + start (alias: `spawn`, removal in v0.2) | `spawn_agent` |
| `stop <agent> [--grace 10s]` | Gracefully stop the container (alias: `kill`, removal in v0.2) | `kill_agent` |
| `restart <agent> [--preserve-session]` | Restart | `restart_agent` |
| `prompt <agent> "<text>"` | Send a prompt | `prompt_agent` |
| `archive <agent>` | Move to archived state | `archive_agent` |

### `sextant conversation <agent> [--tail] [--from-seq N]`

Subscribe to `agents.<uuid>.frames` and print frames in human-readable form. `--tail` exits on session-end lifecycle; otherwise streams forever.

### `sextant ask <agent> "<text>" [--timeout 60s] [--json]`

Synchronous one-shot: subscribe to `agents.<uuid>.frames` + `agents.<uuid>.lifecycle`, publish a prompt via `prompt_agent`, then stream the agent's reply inline until the next `lifecycle transition=turn_ended` (or `transition=ended`) for that agent. Exits 0 on a clean turn finish; exits non-zero with a clear "timeout waiting for turn_ended" message on `--timeout` expiry. `--timeout` defaults to 60s. `<agent>` accepts a name or a UUID (same resolution as `sextant agents archive`). `--json` swaps to NDJSON output, same shape as `sextant conversation --json`.

Use this for daily-drive assistant chats where the two-pane `sextant conversation ... &` + `sextant agents prompt ...` workflow is overkill. The verb subscribes BEFORE publishing the prompt so the first frame can't be missed.

### Deprecated CLI verb aliases (one-release backwards compat)

The 2026-05-27 closed-exception verb migration renamed four verbs to the default CRUD vocabulary. The old spellings continue to resolve via cobra aliases for one release and are scheduled for removal in v0.2:

| Old | New |
|---|---|
| `sextant agents spawn` | `sextant agents create` |
| `sextant agents kill` | `sextant agents stop` |
| `sextant audit query` | `sextant audit list` |
| `sextant worktree destroy` | `sextant worktree delete` |

The wire RPC verb names (`spawn_agent`, `kill_agent`, `query_audit`, `worktree_destroy`) are unchanged — this is a CLI-surface rename only. See `conventions/tui-conventions.md` § "Command design → Fixed verb vocabulary" and `slug:feat-cli-verb-vocabulary-decision` for rationale.

### `sextant pending`

Lists user-input requests across all agents. Sub-verbs:

- `sextant pending list [--since 1h]` — show queue
- `sextant pending answer <request_id> "<answer>"` — answer one
- `sextant pending defer <request_id>` — defer to operator
- `sextant pending escalate <request_id> --to <agent>` — escalate

**`pending list` implementation (M12)**: subscribe to `user_input.requests.>` with `DeliverAll` for a bounded snapshot of the `user_input` JetStream stream (default retention 30 days, see `pkg/natsboot/layout.go`), then collect every request envelope that does not have a matching `user_input.responses.<request_id>` envelope in the same snapshot. Returns the unanswered queue. The `--since` flag clamps the lookback window. Streaming-live mode is deferred — M12 ships the snapshot form.

The pending answer/defer/escalate verbs publish `kind=user_input_response` envelopes on `user_input.responses.<request_id>` with the appropriate `decision` field per `sextantproto.UserInputResponsePayload`.

### `sextant files <verb>`

| Verb | Purpose | RPC |
|---|---|---|
| `read <agent> <path>` | Read file from container | `read_file` |
| `ls <agent> <path>` | List directory | `list_dir` |
| `tail <agent> <path>` | tail -f over RPC | `read_file_stream` |

### `sextant exec <agent> -- <cmd> [args...]`

Run command inside agent's container. Capability-gated (operator-level). Audited.

### `sextant audit <verb>`

| Verb | Purpose | RPC |
|---|---|---|
| `list [--since 1h] [--actor X] [--action spawn]` | Filter audit log (alias: `query`, removal in v0.2) | `query_audit` |
| `tail [--filter ...]` | Live audit stream | NATS subscribe on `audit.>` |

`query_audit` is a dedicated RPC verb (not `query_history`): it targets the ClickHouse `audit` table directly so the column shape matches `pkg/shipper/mapping.go::AuditRow` (actor, action, capability_required, result, payload) rather than the generic envelope shape. Pinned for M12; the M10 MCP tool `query_audit` switches to this new verb so both surfaces share one backend.

### `sextant tail <subject> [--from-seq N] [--json]`

Subscribe to an arbitrary NATS subject and print envelopes as they arrive. `audit tail` and `conversation` are narrow special-cases of this — `tail` exposes the same `pkg/client.Subscribe` machinery for any subject pattern so operators don't need a separate `nats` CLI install for ad-hoc bus observation.

Subjects accept NATS wildcards (`*` matches one token, `>` matches one or more). Common patterns:

| Pattern | Use |
|---|---|
| `agents.>` | every agent's events (frames, lifecycle, heartbeat, inbox) |
| `agents.*.lifecycle` | lifecycle transitions across all agents |
| `telemetry.>` | OTel firehose |
| `sextant.system.>` | daemon self-management events |
| `audit.>` | audit log (equivalent to `sextant audit tail`) |

`--from-seq N` rebinds the consumer at JetStream stream sequence `N` so an operator can gap-fill after a disconnect. `--json` swaps the default human-readable renderer for raw envelope JSON, one per line (NDJSON).

A JetStream ordered consumer binds to exactly one stream, so the subject must resolve to a single stream. A bare `>` firehose spans every stream and is not subscribable as one consumer — use a stream-scoped prefix.

### `sextant traces show <trace_id>`

Display a full distributed trace via spans from ClickHouse.

### `sextant worktree <verb>` (post-M14)

| Verb | Purpose |
|---|---|
| `list` | Show all worktrees |
| `create <name> [--base main]` | Create new worktree |
| `delete <name>` | Clean up worktree (alias: `destroy`, removal in v0.2) |
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
- `10` — no results found (distinct from real errors so shell loops can branch). Verbs that surface empty-result-set as 10: `sextant agents list`, `sextant pending list`. More to follow.

## Open

- Tab-completion shipping — generate per-shell completions on `sextant init`?
- Config override via env vars — `SEXTANT_NATS_URL=...` etc. — lean yes
- Plugin commands — third-party CLI verbs via `~/.config/sextant/plugins/`? Probably v2.
