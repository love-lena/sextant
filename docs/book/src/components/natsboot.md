# natsboot

**Source**: `pkg/natsboot/`, `cmd/sextant-natsboot/`.

`natsboot` starts a single-host `nats-server` subprocess with JetStream enabled, and idempotently creates every stream and KV bucket sextant relies on.

## When to reach for this component

- You're building a test harness that needs a real NATS instance.
- You want to know exactly which streams + buckets the system depends on.
- You're investigating "stream not found" or "bucket not found" errors at startup.

## Public surface

| Symbol                          | File:line                       | Purpose                                                |
|---------------------------------|---------------------------------|--------------------------------------------------------|
| `Server`                        | `pkg/natsboot/server.go`        | Subprocess handle: `Start`, `Stop`, `Wait`, `Connect`. |
| `NewServer(cfg Config)`         | `pkg/natsboot/server.go`        | Construct.                                             |
| `Config`                        | `pkg/natsboot/config.go`        | Listen host/port, operator creds, data dir, log file.  |
| `DefaultConfig(dataDir)`        | `pkg/natsboot/config.go`        | Factory with sextant defaults.                         |
| `Bootstrap(ctx, nc)`            | `pkg/natsboot/bootstrap.go`     | Create or update every stream + KV bucket.             |
| `VerifyBootstrap(ctx, nc)`      | `pkg/natsboot/bootstrap.go`     | Assert each stream + bucket exists.                    |
| `Streams(maxBytes)`             | `pkg/natsboot/layout.go`        | Static list of stream specs.                           |
| `KVBuckets()`                   | `pkg/natsboot/layout.go`        | Static list of KV bucket specs.                        |

## Streams (created by `Bootstrap`)

Eleven streams, defined in `pkg/natsboot/layout.go:Streams()`:

| Stream             | Subjects matched         | Max age   |
|--------------------|--------------------------|-----------|
| `agent_frames`     | `agents.*.frames`        | 7 days    |
| `agent_lifecycle`  | `agents.*.lifecycle`     | 30 days   |
| `agent_heartbeats` | `agents.*.heartbeat`     | 1 hour    |
| `agent_inbox`      | `agents.*.inbox`         | 24 hours  |
| `audit`            | `audit.>`                | 365 days  |
| `telemetry_traces` | `telemetry.traces.>`     | 7 days    |
| `telemetry_metrics`| `telemetry.metrics.>`    | 30 days   |
| `telemetry_logs`   | `telemetry.logs.>`       | 7 days    |
| `user_input`       | `user_input.>`           | 30 days   |
| `control_rpc`      | `sextant.rpc.>`          | 24 hours  |
| `system`           | `sextant.system.>`       | 30 days   |

All retention is `limits` (max-age / max-bytes). `Bootstrap` uses `CreateOrUpdateStream` so re-running the daemon on existing data is safe.

## KV buckets

| Bucket               | Purpose                                          |
|----------------------|--------------------------------------------------|
| `agent_definitions`  | Durable agent records keyed by UUID.             |
| `agent_incarnations` | Live process records keyed by incarnation UUID.  |
| `templates`          | Synced from `~/.config/sextant/templates/*.toml`. |
| `viz_specs`          | Reserved for the viz-spec mechanism (not active). |
| `ui_state`           | Per-operator UI coordination keys (`<operator>.<field>`). |
| `worktrees`          | Worktree registry, keyed by worktree name.       |
| `locks`              | Merge and deploy locks (`merge`, `deploy`).      |
| `test_envs`          | Reserved for M17 test environments (not active). |

## Standalone binary

`cmd/sextant-natsboot/` is a thin wrapper that lets you bring up a sextant-shaped NATS instance from the shell — useful for testing clients without spinning up the full daemon.

```bash
sextant-natsboot --data-dir /tmp/nats --listen 127.0.0.1:4222
```

## Subprocess details

`Server.Start` execs `nats-server -c <generated-config-file>` and parses stdout for the readiness line. The config file is generated from `Config` and includes JetStream storage paths, the two listeners (Unix-socket-less in practice — see [sextantd](./sextantd.md) §Sockets), and operator credentials. The subprocess is in its own process group so `Stop` can deliver SIGTERM cleanly.

## Test coverage

`pkg/natsboot/natsboot_test.go` exercises a full Start → Bootstrap → Publish → Consume → Stop cycle. A separate leak test asserts no leftover `nats-server` subprocesses after teardown.
