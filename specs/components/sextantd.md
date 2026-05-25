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

## Binaries in this scope

There are two binaries: `sextant` (operator CLI) and `sextantd` (daemon). Both live in this repo at `cmd/sextant/` and `cmd/sextantd/` respectively. `init` and `doctor` are subcommands of `sextant`, not `sextantd`.

## sextantd subcommands

- `sextantd` (no args) — main daemon mode: runs the supervisor loop until SIGTERM
- `sextantd --test-mode --test-id=<uuid>` — test mode: runs in a namespaced config dir, listens on isolated ports (M17)

## sextant CLI relationship

- `cmd/sextant/` ships in M5 with `init` and `doctor` so M5 acceptance (`sextant init && sextantd`) works.
- Additional verbs (`agents`, `conversation`, `pending`, ...) land across M11 and M12.

## Startup sequence

1. Load config from `~/.config/sextant/sextantd.toml`
2. Verify CA keypair exists; refuse to start if not (operator must run `sextant init` first — the `sextant` binary is the one that creates the CA)
3. Start NATS subprocess; wait for ready
4. Start ClickHouse subprocess; wait for ready; apply migrations
5. Start `sextant-shipper` as a supervised subprocess (the NATS→ClickHouse pipeline). Default-on via `[shipper] auto_supervise = true`; operators that prefer launchd/systemd flip it off and run `sextant-shipper` standalone. Binary is resolved from the sextantd binary's directory first, then from PATH. The shipper reads `~/.config/sextant/shipper.toml` and discovers live NATS/ClickHouse addresses from `runtime.json`.
6. Sync templates from `~/.config/sextant/templates/*.toml` into the `templates` NATS KV bucket (M11+). The spawn handler resolves templates by name from KV, so the directory and the bucket must agree at boot. Idempotent — re-writing a key with the same value is a no-op.
7. Register supervisor heartbeat
8. Start MCP server
9. Start RPC server
10. Restore agent state from NATS KV (any agents in `running` state get reconciled)
11. Signal ready; enter supervision loop

## Supervision loop

- Watch each subprocess (NATS, ClickHouse, shipper) via process exit channels
- On unexpected exit: log + audit, restart with exponential backoff (1s, 2s, 4s, ..., cap 5min)
- On 5 consecutive restart failures: enter quarantine — daemon emits a critical alert event and stops auto-restarting
- Watch each agent container similarly; restart per agent's definition policy

## Shutdown sequence

Reverse-dependency order: tear consumers down before their substrate. The intent is that a process that's about to die never gets a chance to publish to a half-torn-down dependency.

1. Stop running agent incarnations (Docker `stop`).
2. Drain MCP server, then RPC server, then containermgr.
3. `shipperSup.Stop(ctx)` — the shipper reads from NATS and writes to ClickHouse, so it goes before either substrate. Process-group SIGTERM → SIGKILL, mirroring NATS/ClickHouse.
4. `chSup.Stop(ctx)` — process-group kill (the leader's watchdog child must die with it; see commit 6c05784).
5. `natsSup.Stop(ctx)` — same pattern.
6. Cancel the shared supervisor context, drain supervisor goroutines.
7. Belt-and-suspenders: `stopShipperNow` → `stopClickHouseNow` → `stopNATSNow` to reap any subprocess the supervisor missed.

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

### M11 spawn flow

1. **Validate**: assert the requested `name` is unique among non-archived agents in the `agent_definitions` KV bucket.
2. **Resolve template**: look up the requested template in the `templates` KV bucket (seeded from `~/.config/sextant/templates/` on `sextant init`).
3. **Build AgentDefinition**: fresh UUID; runtime config from template; sandbox image from template; tool allowlist from template's `permissions`; host_pin from request or "local"; lifecycle = `defined`; version = 1.
4. **Persist definition**: `agent_definitions.<uuid>` KV entry.
5. **Append history**: write the initial row into the ClickHouse `agent_definitions_history` table.
6. **Issue JWT**: per-incarnation JWT signed by the M5 CA carrying the template's `permissions` as the `sxt_caps` claim, with `sub = <agent_uuid>` and `sxt_inc = <incarnation_id>`. Lifetime pinned at 24h for M11 (the value lives in `pkg/rpc/handlers.SpawnJWTLifetime`; bump there and re-test). M16's self-update flow promotes this to a configurable knob once a real consumer needs longer-lived tokens.
7. **Build container spec**: image from template, env vars per `specs/components/sidecar-image.md` §"Env vars", workspace mount per the §"Container management" entry above. M11 uses a per-agent stop-gap workspace at `~/.local/share/sextant/spawn-workspaces/<agent_uuid>/` (mkdir'd on demand by the spawn handler); real git worktrees land in M14. The directory is intentionally per-agent (not per-incarnation) so a restart preserves whatever the previous incarnation wrote.
8. **Start container**: via the Docker SDK with labels `sextant.agent_uuid`, `sextant.agent_name`, `sextant.host_id`, `sextant.incarnation_id`.
9. **Persist incarnation**: `agent_incarnations.<incarnation_id>` KV entry with `state = starting`, container ID, started_at.
10. **Promote definition lifecycle to `running`** (version bumps to 2) and re-write the KV entry.

### Shutdown — running incarnations

On graceful sextantd shutdown (SIGTERM), every running incarnation receives a Docker `stop` with the per-template grace period (default 10s, falling back to a SIGKILL after the deadline). The corresponding `agent_incarnations.<incarnation_id>` KV entry is updated with `state = exited` and `ended_at` set. Agent definitions stay at `running` so the next sextantd boot can reconcile (Phase-1 takes the simple route: incarnations are not auto-restarted on reboot; the operator re-spawns).

The daemon enumerates live incarnations by listing the `agent_incarnations` KV bucket and stopping every entry whose `state` is `starting` or `ready` and `container_id` is non-empty. Stop ordering: containers first, then MCP/RPC/supervisors. The intent is that containers can no longer reach the bus or MCP before we tear those substrates down — otherwise a sidecar's last heartbeat blocks NATS shutdown.

## Control socket

Sextantd opens a Unix domain socket at `~/.local/share/sextant/sextantd.sock` (mode `0600`, owned by the operator's Unix user). The socket is the trust boundary for operator-only control surfaces that bypass NATS — primarily liveness probing today and proper RPC dispatch from M7 onward.

**Protocol — M5**: line-oriented text. On accept, the daemon writes one line `OK <version>\n` and closes the connection. `<version>` is `sextantd/<git-sha-or-version>`. `sextant doctor` connects, reads the line, and treats `OK` as "daemon is running".

**Protocol — M7+**: the same socket carries newline-delimited JSON-RPC 2.0 frames for operator-authority RPCs (the NATS path is for agent-authority RPCs). The M5 greeting line stays as the connection handshake.

Considered: pure JSON-RPC from M5. Rejected because M5's only consumer is `sextant doctor` and a text greeting is trivially testable from any tool; the extra framing would only pay off in M7.

## MCP server (M10)

Sextantd hosts the MCP server in-process per `architecture.md` §9c. Two transports run side-by-side, each with its own trust boundary:

| Transport | Listener | Auth | Caller authority |
|---|---|---|---|
| Streamable HTTP | `mcp.http_host:mcp.http_port` at path `/mcp` (default `127.0.0.1:5172`) | `Authorization: Bearer <jwt>` verified against the M5 CA on every HTTP request | Agent: `Caller{Kind:"agent", Capabilities: claims.Capabilities}` |
| stdio over Unix socket | `~/.local/share/sextant/sextantd-mcp.sock` (mode `0600`) | none — Unix file perms are the trust boundary per §10b | Operator: `Caller{Kind:"operator"}` (all caps) |

The stdio socket is distinct from the control socket so the two protocols (newline JSON-RPC for daemon control, MCP framing for tool calls) never multiplex on the same FD. Each accepted Unix-socket connection becomes one MCP session — read/write framing is the MCP stdio transport, just plumbed over a `net.UnixConn` instead of process pipes.

The HTTP listener binds to `127.0.0.1` by default; binding to `0.0.0.0` for multi-host federation is a v2 deployment concern and the JWT verification is the load-bearing security boundary regardless of bind address.

**Tool authorization**: every tool registered in the MCP server declares a required capability string. For agent callers (HTTP transport), the server asserts `slices.Contains(caller.Capabilities, tool.Capability)` before invoking the handler; mismatch returns a tool error with `code="capability_denied"` and `details.capability_required=<cap>`. For operator callers (stdio transport), the cap check is bypassed — Unix-perm trust holds.

**Tool-call audit**: every tool invocation (success, error, denied) emits one `audit.tool_call` envelope to NATS. The payload is the `AuditPayload` struct with `Action="tool_call.<name>"`, `CapabilityRequired=<cap>`, `Result` one of `allowed`|`denied`|`error`, and `Details` carrying `tool`, `caller_kind`, `caller_id`, `duration_ms`, optional `error_code`. The envelope chains to the agent's trace if the caller supplies one in the MCP `_meta.trace_id` field; otherwise a fresh trace starts at the tool call.

**Initial tool catalog (M10)**: communication (`send_message`, `broadcast`), introspection (`list_agents`, `agent_status`, `query_audit`), control (`spawn_agent`, `kill_agent`, `prompt_agent` — stubbed, real impl in M11), system (`emit_event`, `get_metric`). Capabilities follow the `rpc-catalog.md` table where the verb exists; new MCP-only capability names (`send_message`, `broadcast`, `emit_event`, `read.metrics`) are introduced in this milestone.

## sextantd.toml schema

Operator-edited config at `~/.config/sextant/sextantd.toml` (mode `0600`). Generated by `sextant init`; re-run to regenerate (with `--force`). All keys are optional; defaults shown below apply when absent.

```toml
[daemon]
control_socket  = "~/.local/share/sextant/sextantd.sock"  # Unix socket path
shutdown_timeout = "30s"                                  # graceful shutdown cap
restart_backoff_initial = "1s"                            # first restart wait
restart_backoff_max     = "5m"                            # backoff ceiling
restart_quarantine_after = 5                              # consecutive failures → quarantine

[ca]
key_path = "~/.config/sextant/ca.key"  # ED25519 private key (mode 0600)
pub_path = "~/.config/sextant/ca.pub"  # ED25519 public key  (mode 0644)

[nats]
data_dir         = "~/.local/share/sextant/nats"
listen_host      = "127.0.0.1"
listen_port      = 0                                  # 0 = auto-pick a free port
operator_creds   = "~/.config/sextant/operator.creds" # operator credentials (mode 0600)
log_file         = ""                                 # empty = discard

[clickhouse]
data_dir   = "~/.local/share/sextant/clickhouse"
listen_host = "127.0.0.1"
http_port   = 0                                      # 0 = auto
tcp_port    = 0                                      # 0 = auto
database    = "sextant"
user        = "sextant"
password_file = "~/.config/sextant/clickhouse.password"  # mode 0600
log_file    = ""

[mcp]
http_host    = "127.0.0.1"
http_port    = 5172                                  # pinned default; see §"MCP server"
stdio_socket = "~/.local/share/sextant/sextantd-mcp.sock"

[shipper]
auto_supervise = true                                 # default; false = run sextant-shipper standalone
binary_path    = ""                                   # empty = sibling of sextantd binary, then PATH
config_path    = "~/.config/sextant/shipper.toml"     # passed via --config
log_file       = ""                                   # empty = /dev/null

[paths]
templates_dir = "~/.config/sextant/templates"
client_config = "~/.config/sextant/client.toml"
```

`~/` expands against `os.UserHomeDir()`. Empty string means "use default."

The NATS listen port and ClickHouse ports default to `0` (auto-allocated) so first-run does not collide with anything already on the box; once the daemon binds, the actual port is recorded in `~/.local/share/sextant/runtime.json` for downstream consumers (`client.toml` rewriting at daemon start time is M7's job — for M5, `client.toml` carries the default port and `sextant doctor` reads `runtime.json` to find the live port).

## operator.creds format

`~/.config/sextant/operator.creds` is the NATS operator-user credentials file written by `sextant init` (mode `0600`). For M5 the file is a small TOML document; the matching `nats.UserCredentials()` adapter that NATS expects (an opaque JWT bundle) lands in M11 when agents start connecting. The TOML form keeps M5 self-contained while preserving the same path for M11 to overwrite.

```toml
# ~/.config/sextant/operator.creds — mode 0600
user     = "operator"
password = "<32-byte URL-safe random>"
```

`pkg/client` reads this file via the `creds_path` field of `client.toml`. The current loader expects the TOML form above; M11 introduces a real NATS creds bundle and updates both writer (`sextant init`) and reader (`pkg/client`) atomically.

## Default data layout

| Path | Mode | Purpose |
|---|---|---|
| `~/.config/sextant/` | `0700` | All operator config |
| `~/.config/sextant/ca.key` | `0600` | CA private key (ED25519 seed) |
| `~/.config/sextant/ca.pub` | `0644` | CA public key |
| `~/.config/sextant/sextantd.toml` | `0600` | Daemon config |
| `~/.config/sextant/client.toml` | `0600` | Client library config |
| `~/.config/sextant/operator.creds` | `0600` | NATS operator credentials |
| `~/.config/sextant/clickhouse.password` | `0600` | ClickHouse user password |
| `~/.config/sextant/templates/` | `0700` | Agent templates (one TOML per template) |
| `~/.local/share/sextant/` | `0750` | All daemon-managed runtime state |
| `~/.local/share/sextant/nats/` | `0750` | NATS JetStream data |
| `~/.local/share/sextant/clickhouse/` | `0750` | ClickHouse data |
| `~/.local/share/sextant/shipper-buffer/` | `0750` | M6 shipper buffer |
| `~/.local/share/sextant/test/` | `0750` | M17 test envs |
| `~/.local/share/sextant/sextantd.sock` | `0600` | Daemon control socket |
| `~/.local/share/sextant/sextantd-mcp.sock` | `0600` | MCP server stdio socket (operator-only, M10) |
| `~/.local/share/sextant/runtime.json` | `0600` | Live runtime details (ports, pid) |
| `~/.local/share/sextant/spawn-workspaces/` | `0750` | M11 stop-gap workspace mounts (`<agent_uuid>/` per agent); replaced by git worktrees in M14 |

## Supervision details

Each managed subprocess (NATS, ClickHouse, M6's shipper) runs under its own `pkg/supervisor.Supervisor`. On unexpected exit the supervisor restarts the subprocess with backoff; the daemon never silently sits on a dead unit.

Backoff schedule: `restart_backoff_initial` (default 1s), doubling per consecutive failure, capped at `restart_backoff_max` (default 5min). Counter resets when a subprocess stays up for at least `restart_backoff_max` continuous seconds.

Quarantine: after `restart_quarantine_after` consecutive restart failures (default 5), the supervisor stops auto-restarting that subprocess and returns an error from `Supervisor.Run`. The daemon then logs `<unit> QUARANTINED ...`, signals its own done channel, and exits non-zero so an outer process supervisor (systemd, launchd, or the operator's shell) can take over. An `audit.sextantd` envelope with action `subprocess_quarantined` and result `error` lands once M7's audit-publishing path is wired; M5 logs to stderr only.

**Port stability across restarts**: ports allocated dynamically by the kernel (port 0 in the config) are captured on first boot and reused on every subsequent restart, so reconnecting clients keep the same endpoint and `runtime.json` does not lie. `runtime.json` is re-written after each restart to refresh the subprocess PIDs.

**Bootstrap idempotency on restart**:
- NATS: JetStream rehydrates streams + KV from the on-disk data dir. The daemon re-runs `natsboot.Bootstrap` (CreateOrUpdate semantics) after each restart so any schema drift surfaces immediately.
- ClickHouse: data files survive restart. The daemon re-runs `clickhouseboot.Apply` (idempotent on SHA-tracked migrations) for the same reason.

**Operator NATS reconnection**: `pkg/client` is configured with `MaxReconnects(-1)` and bounded `ReconnectWait` + jitter, so a NATS subprocess restart looks like a connection blip to the daemon's own NATS clients. Connectivity returns once the listener is re-bound; in-flight subscriptions auto-resume from their last-acked `StreamSeq`.

## Open

- Detailed RPC handler implementations — separate per-verb spec files later
- Watchdog process — separate binary (`cmd/sextant-watchdog/`) or sextantd-spawned? Lean: separate to survive sextantd's own swap
- `runtime.json` schema — minimal in M5 (`nats_url`, `clickhouse_tcp`, `pid`, `started_at`); promoted to a typed package once a second reader appears.
