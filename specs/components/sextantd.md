# sextantd — component spec

## Role

The supervisor daemon. Sextantd owns:
- Process supervision (NATS, ClickHouse, shipper)
- Signing CA (issues per-agent JWTs)
- Control RPC endpoint (over NATS request/reply)
- MCP server (exposes sextant tools to SDK sidecars)
- Container spawning (via Docker SDK)
- Self-update flow (execv handoff, watchdog coordination)

See `architecture.md` §5 (supervisor), §6 (control plane), §9c (MCP), §10 (auth), §12 (self-update).

## Layout

Go binary at `cmd/sextantd/`. Internal packages under `pkg/`:

```
cmd/sextantd/                # main daemon entry point
pkg/
├── client/                  # sextant-client-go (also consumed externally)
├── sextantproto/            # shared types (M1)
├── natsboot/                # NATS bootstrap (M2)
├── clickhouseboot/          # ClickHouse bootstrap (M3)
├── authjwt/                 # JWT issuance + verification (M5)
├── supervisor/              # process supervision loop
├── containermgr/            # Docker SDK wrapper
├── mcpserver/               # MCP server (M10)
├── rpc/                     # RPC dispatch + handlers (M7)
├── shipper/                 # NATS → ClickHouse (M6, possibly separate cmd)
├── testenv/                 # test environment provisioner (M17)
└── theme/                   # shared theme tokens for TUIs
```

## Subcommands

- `sextant init` — first-run setup: generates CA keypair, writes config, creates data dirs, bootstraps NATS + ClickHouse
- `sextantd` (no args) — main daemon mode: runs the supervisor loop until SIGTERM
- `sextantd --test-mode --test-id=<uuid>` — test mode: runs in a namespaced config dir, listens on isolated ports (M17)

The `sextant` CLI (M11/M12) is a separate binary at `cmd/sextant/`.

## Startup sequence

1. Load config from `~/.config/sextant/sextantd.toml`
2. Verify CA keypair exists; refuse to start if not (operator must run `sextant init` first)
3. Start NATS subprocess; wait for ready
4. Start ClickHouse subprocess; wait for ready; apply migrations
5. Start shipper subprocess; wait for ready
6. Register supervisor heartbeat
7. Start MCP server
8. Start RPC server
9. Restore agent state from NATS KV (any agents in `running` state get reconciled)
10. Signal ready; enter supervision loop

## Supervision loop

- Watch each subprocess (NATS, ClickHouse, shipper) via process exit channels
- On unexpected exit: log + audit, restart with exponential backoff (1s, 2s, 4s, ..., cap 5min)
- On 5 consecutive restart failures: enter quarantine — daemon emits a critical alert event and stops auto-restarting
- Watch each agent container similarly; restart per agent's definition policy

## Signal handling

| Signal | Behavior |
|---|---|
| SIGTERM | Graceful shutdown — drain agent traffic, persist state, stop subprocesses in reverse startup order |
| SIGUSR2 | Execv handoff to new binary at staging path (self-update, M16) |
| SIGHUP | Re-read config — non-disruptive for most settings |

## Container management

Uses the Docker SDK for Go (`github.com/docker/docker/client`). Creates containers from `sextant-sidecar:<version>` image with:
- Mounts: worktree path → `/workspace`, per-agent named volumes for `~/.claude`, `~/.cargo`, `~/.npm`, etc.
- Env vars: agent UUID, NATS connection string (using host networking on macOS via OrbStack), JWT
- Network: host or default bridge; per-agent egress policy (initial: open by default)
- Resource limits: CPU/memory caps from agent definition
- Labels: `sextant.agent_uuid`, `sextant.agent_name`, `sextant.host_id` for discovery + cleanup

## Open

- Detailed RPC handler implementations — separate per-verb spec files later
- Watchdog process — separate binary (`cmd/sextant-watchdog/`) or sextantd-spawned? Lean: separate to survive sextantd's own swap
- Operator socket protocol — direct NATS or a separate Unix socket for the operator's control? Lean: NATS, no separate socket
