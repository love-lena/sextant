# Process model

A picture of what runs where, who supervises whom, and how starts and stops are sequenced.

## The tree at runtime

```
sextantd                                                  [host process]
├── nats-server                                          (supervised subprocess)
├── clickhouse-server                                    (supervised subprocess)
├── sextant-shipper                                      (supervised subprocess, optional)
└── in-process goroutines:
    ├── RPC server (NATS request/reply on sextant.rpc.*)
    ├── MCP server: Streamable HTTP listener on :5172
    ├── MCP server: stdio Unix socket
    └── operator control socket (~/.local/share/sextant/sextantd.sock)

dockerd                                                  [system daemon]
└── sextant-sidecar:<tag> containers                     (one per agent incarnation)
    └── node /opt/sextant/sidecar/dist/index.js run      (the sidecar entrypoint)
```

`sextantd` is the only host process that supervises anything; everything else either runs inside `sextantd` (RPC, MCP, control socket) or is a Docker-managed container.

## Startup sequence

From `cmd/sextantd/daemon.go`:

1. Parse flags, load `sextantd.toml`, resolve all paths.
2. Load CA keypair from disk via `pkg/authjwt.LoadCA`.
3. Start `nats-server` via `pkg/natsboot.Server.Start`. Block until "Server is ready". Capture the dynamic port.
4. Run `pkg/natsboot.Bootstrap`: idempotently create every stream and KV bucket.
5. Start `clickhouse-server` via `pkg/clickhouseboot.Server.Start`. Block until "Ready for connections".
6. Run `pkg/clickhouseboot.Apply`: idempotent migration runner; embedded SQL files at `pkg/clickhouseboot/migrations/`.
7. Open the operator control socket at `daemon.control_socket` (mode 0600).
8. Write `runtime.json` with live NATS/ClickHouse ports + the daemon PID.
9. Wrap NATS, ClickHouse, and (if `shipper.auto_supervise = true`) the shipper subprocess in supervisors. Forward supervisor events to a draining goroutine that logs starts/restarts/quarantines.
10. Start the MCP server (`pkg/mcpserver`). Bind both the HTTP listener (default `127.0.0.1:5172`) and the stdio Unix socket (default `<data_dir>/sextantd-mcp.sock`).
11. Start the RPC server (`pkg/rpc/server.go`). Restore agent state by walking the `agent_definitions` and `agent_incarnations` KV buckets.
12. Wire `containermgr` and `worktree` managers into the MCP server via `SetSpawnDeps` / `SetWorktree` so tool handlers can spawn / kill / merge.
13. Enter the signal-wait loop.

## Shutdown sequence

On `SIGTERM`/`SIGINT` (signal loop at `cmd/sextantd/main.go:87-105`; shutdown coordination at `cmd/sextantd/main.go:81-137`):

1. Cancel the root context; stop accepting new RPC / MCP requests.
2. Stop the MCP HTTP listener (5s grace before force-close — `pkg/mcpserver/server.go:394`).
3. Stop the MCP stdio acceptor.
4. Close the operator control socket.
5. Stop the RPC server.
6. Stop the shipper supervisor (signals child `sextant-shipper`, waits, escalates to SIGKILL).
7. Stop ClickHouse, then NATS.
8. Wait for all goroutines to drain (`daemon.shutdown_timeout`, default `30s` — `pkg/sextantd/config.go:166`).
9. Exit.

Active agent containers are **not** killed by `sextantd` shutdown. They keep running with their existing JWTs. When `sextantd` comes back, they reconnect to NATS and resume publishing.

## Supervisor policy

Every supervised subprocess uses `pkg/supervisor` with this policy (defaults at `pkg/supervisor.DefaultPolicy`, overridden via `sextantd.toml`):

| Parameter                | Default     | Source field                              |
|--------------------------|-------------|-------------------------------------------|
| Initial restart backoff  | `1s`        | `daemon.restart_backoff_initial`          |
| Max restart backoff      | `5min`      | `daemon.restart_backoff_max`              |
| Quarantine after         | 5 failures  | `daemon.restart_quarantine_after`         |
| Reset uptime threshold   | tied to max backoff (`5min` default) | wired from `daemon.restart_backoff_max` in `cmd/sextantd/daemon.go` |

Backoff grows exponentially (1s → 2s → 4s → … → 5min). Five consecutive failures (no uptime-reset window in between) puts the unit in `quarantined` — `sextantd` stops restarting it and emits a quarantine event. Operator intervention is required.

## Health probes

The daemon does not run an HTTP `/health` endpoint. Instead:

- The **operator control socket** is a Unix socket; `sextant doctor` round-trips a no-op request to confirm `sextantd` is alive.
- **NATS reachability** is probed by connecting and pinging.
- **ClickHouse reachability** is probed by `SELECT 1`.
- **Agent heartbeats** flow on `agents.<uuid>.heartbeat` every 5 seconds. The architecture spec describes a "missed heartbeat → restart" loop in §5 (Supervisor model); the snapshot publishes heartbeats from the sidecar but does not yet act on missed heartbeats from the supervisor side.
