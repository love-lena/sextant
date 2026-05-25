# First run

The three-step flow from a fresh install to a running agent:

```bash
sextant init      # generate config + CA keys + default template
sextantd &        # start the daemon (NATS, ClickHouse, shipper, MCP, RPC)
sextant doctor    # confirm the stack is healthy
sextant agents spawn assistant --template default
```

## `sextant init`

Performs first-run setup. The subcommand lives at `cmd/sextant/init.go` (dispatched from `cmd/sextant/main.go:46`).

It creates:

- The config directory `~/.config/sextant/` and the data directory `~/.local/share/sextant/`, plus data subdirs (`nats/`, `clickhouse/`, `shipper-buffer/`, `test/`).
- An ed25519 signing keypair at `~/.config/sextant/ca.{key,pub}` (used by `pkg/authjwt` to sign per-incarnation JWTs).
- TOML config files:
  - `~/.config/sextant/sextantd.toml` — daemon configuration (`pkg/sextantd/config.go:13-26`).
  - `~/.config/sextant/client.toml` — client connection details (loaded by `pkg/client/config.go`).
  - `~/.config/sextant/shipper.toml` — shipper configuration.
- `~/.config/sextant/operator.creds` — operator NATS credentials.
- `~/.config/sextant/clickhouse.password` — ClickHouse server password.
- `~/.config/sextant/templates/default.toml` — default agent template (see [Templates](../operator-guide/templates.md)).

`sextant init` is **idempotent** — re-running it preserves existing files unless `--force` is given. Pass `--config-dir` / `--data-dir` to override the canonical paths.

## `sextantd`

Once `init` has produced a usable config, start the daemon:

```bash
sextantd
```

The daemon supervises a series of subprocesses in roughly this order (`cmd/sextantd/daemon.go`):

1. Loads `~/.config/sextant/sextantd.toml`.
2. Loads the signing CA from the configured key paths.
3. Starts `nats-server` as a subprocess via `pkg/natsboot`; waits for "Server is ready", then runs `Bootstrap()` to create JetStream streams and KV buckets.
4. Starts `clickhouse-server` via `pkg/clickhouseboot`; applies any pending migrations from the embedded `migrations/` directory.
5. Opens the operator control socket at `~/.local/share/sextant/sextantd.sock`.
6. Writes `runtime.json` with the live NATS/ClickHouse ports so clients can discover them without re-reading config.
7. Wraps NATS, ClickHouse, and (if `shipper.auto_supervise = true`, the default — `pkg/sextantd/config.go:303-306`) `sextant-shipper` in supervisors.
8. Starts the in-process MCP server (Streamable HTTP on `127.0.0.1:5172` by default, plus a stdio Unix socket — `pkg/sextantd/config.go:190-194`).
9. Starts the RPC server (`pkg/rpc/server.go`); restores agent state by walking the `agent_definitions` and `agent_incarnations` KV buckets.

Every supervised subprocess goes through `pkg/supervisor`, which restarts on exit with exponential backoff (1s → 5min) and quarantines the unit after 5 consecutive failures (defaults at `pkg/sextantd/config.go:167-170`).

The daemon handles signals as follows (`cmd/sextantd/main.go:23, 87-105`):

- **SIGTERM / SIGINT** — graceful shutdown. Subprocesses are stopped in reverse startup order.
- **SIGHUP** — currently logged-only ("not yet implemented (M5)"). Reserved for re-reading config.
- **SIGUSR2** — currently logged-only. Reserved for the M16 self-update execv handoff.

## `sextant doctor`

```bash
sextant doctor
sextant doctor --json
```

Health diagnostics, defined at `cmd/sextant/doctor.go:46`. It checks:

- Config files present and parseable.
- CA keypair exists.
- `sextantd` reachable on its control socket.
- NATS reachable; required streams and KV buckets exist.
- ClickHouse reachable; expected tables exist.
- The installed `sextant` binary's embedded `GitSHA` (from `pkg/version`) matches the daemon's, to catch stale installs.

Exit code `0` if everything is green, `2` if any check fails (`cmd/sextant/main.go:104-108`).

## Spawning your first agent

```bash
sextant agents spawn assistant --template default
sextant agents list
sextant agents prompt assistant "Hello, can you summarize this repo?"
sextant conversation assistant --tail
```

`spawn` instantiates the `default` template (which references the sidecar image), creates an agent record in the `agent_definitions` NATS KV bucket, allocates a new incarnation, issues a JWT, and starts the container with the right env vars and mounts. See [Agent lifecycle](../architecture/lifecycle.md) for the full sequence.

`conversation --tail` subscribes to `agents.<uuid>.frames` and `agents.<uuid>.lifecycle` and exits when the agent emits a `lifecycle.ended` frame.
