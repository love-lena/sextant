# sextant-shipper — component spec

## Role

Subscribe to NATS subjects, write to ClickHouse. At-least-once delivery; ClickHouse tables use `ReplacingMergeTree` so re-deliveries of the same `id` collapse on background merge (or via `FINAL` queries). See `specs/components/clickhouse.md`.

Lives as either a separate process (`cmd/sextant-shipper/`) or a goroutine inside sextantd. **Lean: separate process** for failure isolation — a shipper crash shouldn't take down sextantd. The shipper is its own binary (`cmd/sextant-shipper/`). From the feat-shipper-auto-supervise change onward, sextantd auto-supervises it via `pkg/shipperboot` (default `[shipper] auto_supervise = true` in `sextantd.toml`); the operator opts out with `auto_supervise = false` and runs `sextant-shipper` standalone under launchd/systemd. Same supervisor.Policy as NATS/ClickHouse: exponential backoff, quarantine after 5 consecutive failures.

See `architecture.md` §8 (observability) and §3-layer data architecture for context.

## Subjects → tables mapping

| Subscribes | Writes to | Notes |
|---|---|---|
| `agents.*.frames` | `events` | every agent frame |
| `agents.*.lifecycle` | `events` | lifecycle transitions |
| `agents.*.heartbeat` | `events` | heartbeat envelopes |
| `audit.>` | `audit` | dedicated table for forensics; deep wildcard |
| `telemetry.traces.>` | `telemetry_traces` | OTel span data; deep wildcard |
| `telemetry.metrics.>` | `telemetry_metrics` | OTel metric data; deep wildcard |
| `telemetry.logs.>` | `telemetry_logs` | OTel log records; deep wildcard |
| `user_input.>` | `events` | request/response events; deep wildcard |

Mapping defined as code in `pkg/shipper/mapping.go`. Each NATS subject pattern has a corresponding writer function that constructs the ClickHouse INSERT. One JetStream durable consumer per (stream, pattern). Durable consumer names follow the shape `shipper-<stream>` (e.g. `shipper-events-frames`, `shipper-audit`).

Wildcard discipline matches `specs/protocols/bus-subjects.md`: telemetry subjects always carry an extra host token, so the shipper subscribes with `>` (one-or-more tokens). `agents.*.*` events use `*` (exactly one) because the per-agent subject layout is fixed at `agents.<uuid>.<kind>`.

## Delivery semantics

At-least-once. JetStream consumer groups give us:
- Resumable position (last-acked sequence number)
- Re-delivery on failure
- Multi-consumer parallelism if needed

**Ack ordering**: shipper acks JetStream **only after** the message has been durably written to ClickHouse, or persisted to the BoltDB buffer with a pending-write entry. JetStream is the durable source of truth until ack; BoltDB is finite spillover for ClickHouse-down windows.

ClickHouse dedup via `ReplacingMergeTree(ts)` on `(id)`. Re-inserts with the same `id` collapse on background merge or via `FINAL` on read. Not instant — pipelines that read fresh data should tolerate transient duplicates or use `FINAL` (with the perf cost). Acceptable for initial scale.

## Backpressure handling

When ClickHouse is unreachable or slow:
- Local buffer at `~/.local/share/sextant/shipper-buffer/buffer.db` using BoltDB (`go.etcd.io/bbolt`). Acts as finite spillover; JetStream remains the durable source of truth until ack.
- Per-table buckets (`pending_events`, `pending_audit`, `pending_telemetry_traces`, `pending_telemetry_metrics`, `pending_telemetry_logs`). Each entry's key is a 16-byte monotonic sequence (8-byte epoch nanoseconds + 8-byte sequence number), preserving FIFO order on drain.
- Buffer drains as ClickHouse recovers; drained entries are deleted on successful insert. JetStream messages are acked **after** a row is durably written to ClickHouse OR persisted to BoltDB with a pending-write entry.
- Buffer-depth metric published periodically.
- Hard cap on buffer size (10 GiB default). **On hitting the cap: fail closed** — shipper drains its JetStream consumers (stops pulling new messages), emits a critical `audit.shipper_backpressure` envelope on `audit.shipper_backpressure`, and exits with non-zero status. On graceful shutdown the drain goroutine stops with the rest of the shipper. Any remaining BoltDB entries replay on the next `sextant-shipper` start — the on-disk buffer is the durable substrate across process restarts, so no in-flight work is lost. JetStream's own per-stream max-bytes becomes the limiting factor while sextant-shipper is down. Operator intervention is required (drain via recovery, extend buffer, or enable degraded mode).
- **No silent oldest-event drop.** If the operator wants drop-oldest behavior, it must be explicitly enabled via `shipper.degraded_mode = "drop_oldest"` in config — off by default. Degraded mode emits an `audit.shipper_drop` audit event per drop and increments a drop counter exported as `shipper.dropped_total`.

## Config file

`~/.config/sextant/shipper.toml` (mode `0600`). Written by `sextant init`; loaded by `sextant-shipper`.

```toml
[nats]
url        = "nats://127.0.0.1:4222"
operator_creds = "~/.config/sextant/operator.creds"

[clickhouse]
addr           = "127.0.0.1:9000"
database       = "sextant"
user           = "sextant"
password_file  = "~/.config/sextant/clickhouse.password"

[buffer]
dir            = "~/.local/share/sextant/shipper-buffer"
hard_cap_bytes = 10737418240  # 10 GiB

[batch]
max_events     = 1000
flush_interval = "100ms"
ack_wait       = "30s"        # JetStream AckWait; must exceed flush_interval + ClickHouse write time

[shipper]
degraded_mode  = ""           # "" (default, fail-closed) | "drop_oldest"
metrics_interval = "5s"
service_name   = "sextant-shipper"
host_id        = ""           # empty = os.Hostname()
```

`~/` expands via `os.UserHomeDir()`. Empty `nats.url` and `clickhouse.addr` are populated from `~/.local/share/sextant/runtime.json` at start time (so the shipper picks up the daemon's auto-allocated ports) — but if both `runtime.json` is missing and the config has empty values, startup fails fast.

## Metrics

Shipper publishes its own metrics to `telemetry.metrics.shipper.<host_id>` (matches the `telemetry.metrics.>` stream wildcard) every `metrics_interval`. Envelopes have `Kind = telemetry_metric`; payload is an OTel-shaped `Metric` with `service_name = "sextant-shipper"`.

- `shipper.lag_seconds` — exponential moving average of (write_ts − envelope_ts) per row written in the last interval
- `shipper.buffer_depth_bytes` — current BoltDB file size on disk
- `shipper.write_rate_per_sec` — events written per second over the last interval
- `shipper.errors_total` — categorized error counter (cumulative)
- `shipper.dropped_total` — degraded-mode drop counter (cumulative)

These appear in ClickHouse like any other metric — recursive but harmless because deduped.

## Refined: buffer storage choice

BoltDB via `go.etcd.io/bbolt`. Well-known, embedded, single-file. Per-table buckets; monotonic 16-byte keys for FIFO drain.

## Refined: batching

Default: time-based or size-based, whichever first — every 100 ms OR 1000 events per table. One batch per ClickHouse table (events, audit, telemetry_traces, telemetry_metrics, telemetry_logs). Each table flush is one `INSERT` statement.

## Refined: per-table writer concurrency

One goroutine per table for the flush+drain loop. Inserts and BoltDB drains for a given table never race. Subject-pattern consumers feed into a shared mailbox; the per-table flushers pull from per-table buffers.

## Wire-up to sextantd

Implemented in `pkg/shipperboot` (exec + process-group lifecycle, mirroring `pkg/natsboot` and `pkg/clickhouseboot`). sextantd's startup sequence (specs/components/sextantd.md §"Startup sequence" step 5) spawns the shipper after NATS + ClickHouse are healthy, passing `--config <shipper.toml>` and `--runtime-file <runtime.json>` so the shipper picks up the daemon-allocated NATS / ClickHouse ports. Shutdown reverses the order: shipper first, then ClickHouse, then NATS — same SIGTERM → SIGKILL process-group escalation as the other supervised units.

Binary resolution prefers a sibling of the running sextantd binary (the `go install` and Makefile both drop them in $GOBIN), with a PATH fallback for development shells. Operators that prefer external supervision set `[shipper] auto_supervise = false` in `sextantd.toml`; the daemon then boots without a shipper and the operator runs `sextant-shipper` directly.

## Open

(none — wire-up landed; see feat-shipper-auto-supervise.)
