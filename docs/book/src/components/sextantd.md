# sextantd (daemon)

**Source**: `cmd/sextantd/`, `pkg/sextantd/`.

`sextantd` is the supervisor. It owns the signing CA, runs `nats-server` and `clickhouse-server` as subprocesses, hosts the in-process MCP and RPC servers, and orchestrates agent spawning.

## When to reach for this component

- You're trying to understand what runs when you type `sextantd`.
- You need to tune supervision behaviour, shutdown timeout, or the NATS/ClickHouse port layout.
- You're investigating a startup failure or a stuck shutdown.

If your question is "how do I list / spawn / kill an agent", you don't need this chapter — go to [CLI](../operator-guide/cli.md).

## What it does

| Responsibility                              | Implementation reference                          |
|---------------------------------------------|---------------------------------------------------|
| Supervise NATS + ClickHouse + (optional) shipper | `cmd/sextantd/daemon.go` + `pkg/supervisor`   |
| Bootstrap NATS streams + KV buckets         | `pkg/natsboot.Bootstrap`                          |
| Apply ClickHouse migrations                 | `pkg/clickhouseboot.Apply`                        |
| Sign per-incarnation JWTs                   | `pkg/authjwt.CA`                                  |
| Host RPC dispatch                           | `pkg/rpc/server.go`                               |
| Host MCP server (HTTP + stdio)              | `pkg/mcpserver`                                   |
| Run the spawn flow (Docker + KV + JWT)      | `cmd/sextantd/spawn.go` + `pkg/rpc/handlers/spawn.go` |
| Manage worktrees                            | `cmd/sextantd/worktree.go` + `pkg/worktree`       |
| Restore agent state on restart              | `cmd/sextantd/daemon.go` (KV walk during start)   |

## Configuration

`~/.config/sextant/sextantd.toml`. The Go struct is `pkg/sextantd.Config` (`pkg/sextantd/config.go:17-26`). Sections:

```toml
[daemon]
control_socket            = "~/.local/share/sextant/sextantd.sock"
shutdown_timeout          = "30s"      # default 30s (config.go:166)
restart_backoff_initial   = "1s"
restart_backoff_max       = "5m"
restart_quarantine_after  = 5

[ca]
key_path = "~/.config/sextant/ca.key"
pub_path = "~/.config/sextant/ca.pub"

[nats]
data_dir       = "~/.local/share/sextant/nats"
listen_host    = "127.0.0.1"
listen_port    = 0           # 0 = kernel-picked
operator_creds = "~/.config/sextant/operator.creds"
log_file       = ""           # empty → /dev/null

[clickhouse]
data_dir       = "~/.local/share/sextant/clickhouse"
listen_host    = "127.0.0.1"
http_port      = 0
tcp_port       = 0
database       = "sextant"
user           = "sextant"
password_file  = "~/.config/sextant/clickhouse.password"
log_file       = ""

[mcp]
http_host    = "127.0.0.1"
http_port    = 5172          # default (config.go:192)
stdio_socket = "~/.local/share/sextant/sextantd-mcp.sock"

[shipper]
auto_supervise = true        # default true if [shipper] omitted (config.go:304-306)
binary_path    = ""           # empty = sextantd sibling dir, then PATH
config_path    = ""           # empty = <config_dir>/shipper.toml (config.go:311)
log_file       = ""

[paths]
templates_dir = "~/.config/sextant/templates"
client_config = "~/.config/sextant/client.toml"
runtime_file  = "~/.local/share/sextant/runtime.json"
data_dir      = "~/.local/share/sextant"
config_dir    = "~/.config/sextant"

[worktree]
repo_root      = ""           # empty disables worktree wiring
worktrees_root = "~/.local/share/sextant/worktrees"
prune_interval = "6h"          # how often the auto-pruner fires when enabled
archive_root   = "~/.local/share/sextant/worktree-archive"   # where archived worktrees land
auto_prune     = false         # default off (safe-by-default)
```

`DefaultConfig(configDir, dataDir)` (`pkg/sextantd/config.go:162`) returns the canonical defaults. `LoadConfig(path)` (`pkg/sextantd/config.go:231`) reads from disk, applies defaults for missing fields, expands `~/` in every path field, and validates required fields.

> **Note on `listen_port = 0`**: For NATS, ClickHouse, and MCP, port `0` means "let the kernel pick." `sextantd` then writes the real port to `runtime.json` for clients to discover. Tests use `0`; production installs usually pin explicit ports.

> **Note on `worktree.repo_root`**: empty is the documented "transitional" state — the daemon then skips wiring the worktree manager. To enable agent worktrees, set this to the operator's main checkout (e.g. `~/dev/sextant`) and `worktrees_root` to a sibling directory (e.g. `~/dev/sextant-worktrees`).

## Runtime file

After startup, `sextantd` writes `~/.local/share/sextant/runtime.json` containing:

- The kernel-picked NATS port and TCP URL.
- The kernel-picked ClickHouse HTTP and native ports.
- The daemon PID and start timestamp.

The Go and TS clients prefer `runtime.json` over `client.toml` for the NATS URL (`pkg/client/client.go:119`), so port changes don't require editing client config. The shipper does the same (`pkg/shipper`).

## Sockets

| Path                                            | Owner    | Mode  | Purpose                                  |
|-------------------------------------------------|----------|-------|------------------------------------------|
| `~/.local/share/sextant/sextantd.sock`          | sextantd | 0600  | Operator control (used by `sextant doctor`) |
| `~/.local/share/sextant/sextantd-mcp.sock`      | sextantd | 0600  | MCP stdio transport for operator CLIs/TUIs |
| `~/.local/share/sextant/nats/nats.sock`         | nats-server | 0600 | Reserved: NATS itself has no Unix transport; the file-perm boundary on `operator.creds` is the operator-trust line. |

## Signal handling

| Signal     | Behaviour                                                 |
|------------|-----------------------------------------------------------|
| `SIGTERM`  | Graceful shutdown. Stops everything in reverse start order. |
| `SIGINT`   | Same as `SIGTERM`.                                         |
| `SIGHUP`   | Logged only (M5 stub — `cmd/sextantd/main.go:97`).         |
| `SIGUSR2`  | Logged only (M16 self-update stub — `cmd/sextantd/main.go:98-99`). |

## Test coverage

`cmd/sextantd/*_test.go` has 20+ acceptance tests covering startup, shutdown, NATS supervisor restart, shipper auto-supervise on/off, agent spawn end-to-end (including container exec, worktree creation, and claude-seed propagation), and an orphan-container tripwire (`TestNoOrphanContainersAfterTestSuite`) that fails the suite if any test leaves a `sextant.test_run=*`-labelled container behind.

## Known gaps

- `daemon.shutdown_timeout` default is `30s` here (`pkg/sextantd/config.go:166`); `specs/components/sextantd.md` mentions `10s` in some sections. Trust the code.
- SIGHUP and SIGUSR2 are stubs — see [Known gaps and drift](../reference/known-gaps.md).
- The architecture spec describes a separate watchdog process for self-update. Not implemented at this snapshot.
