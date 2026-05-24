# sextant-shipper — component spec

## Role

Subscribe to NATS subjects, write to ClickHouse. At-least-once delivery; ClickHouse tables use `ReplacingMergeTree` so re-deliveries of the same `id` collapse on background merge (or via `FINAL` queries). See `specs/components/clickhouse.md`.

Lives as either a separate process (`cmd/sextant-shipper/`) or a goroutine inside sextantd. **Lean: separate process** for failure isolation — a shipper crash shouldn't take down sextantd.

See `architecture.md` §8 (observability) and §3-layer data architecture for context.

## Subjects → tables mapping

| Subscribes | Writes to | Notes |
|---|---|---|
| `agents.*.frames` | `events` | every agent frame |
| `agents.*.lifecycle` | `events` | lifecycle transitions |
| `audit.*` | `audit` | dedicated table for forensics |
| `telemetry.traces.*` | `telemetry_traces` | OTel span data |
| `telemetry.metrics.*` | `telemetry_metrics` | OTel metric data |
| `telemetry.logs.*` | `telemetry_logs` | OTel log records |
| `user_input.*` | `events` | request/response events |

Mapping defined as code in `pkg/shipper/mapping.go`. Each NATS subject pattern has a corresponding writer function that constructs the ClickHouse INSERT.

## Delivery semantics

At-least-once. JetStream consumer groups give us:
- Resumable position (last-acked sequence number)
- Re-delivery on failure
- Multi-consumer parallelism if needed

**Ack ordering**: shipper acks JetStream **only after** the message has been durably written to ClickHouse, or persisted to the BoltDB buffer with a pending-write entry. JetStream is the durable source of truth until ack; BoltDB is finite spillover for ClickHouse-down windows.

ClickHouse dedup via `ReplacingMergeTree(ts)` on `(id)`. Re-inserts with the same `id` collapse on background merge or via `FINAL` on read. Not instant — pipelines that read fresh data should tolerate transient duplicates or use `FINAL` (with the perf cost). Acceptable for initial scale.

## Backpressure handling

When ClickHouse is unreachable or slow:
- Local buffer at `~/.local/share/sextant/shipper-buffer/` using BoltDB. Acts as finite spillover; JetStream remains the durable source of truth.
- Buffer drains as ClickHouse recovers; drained entries are then acked on JetStream.
- Buffer-depth metric published periodically.
- Hard cap on buffer size (10GB default). **On hitting the cap: fail closed** — shipper stops pulling from JetStream and emits a critical `audit.shipper_backpressure` event. JetStream's own per-stream max-bytes becomes the limiting factor. Operator intervention is required (drain via recovery, extend buffer, or enable degraded mode).
- **No silent oldest-event drop.** If the operator wants drop-oldest behavior, it must be explicitly enabled via `shipper.degraded_mode = "drop_oldest"` in config — off by default. Degraded mode emits an audit event per drop.

## Metrics

Shipper publishes its own metrics to `telemetry.metrics.shipper.*`:
- `shipper.lag_seconds` — gap between event ts and write ts
- `shipper.buffer_depth_bytes` — current local buffer size
- `shipper.write_rate_per_sec` — events written per second
- `shipper.errors_total` — categorized error counter

These appear in ClickHouse like any other metric — recursive but harmless because deduped.

## Open

- Buffer storage choice: BoltDB, BadgerDB, raw files? Lean: BoltDB (well-known, embedded, single-file)
- Batching: how many events per ClickHouse INSERT? Larger batches → better throughput but worse lag. Default: time-based batching (every 100ms or 1000 events)
- Per-table writer concurrency: one goroutine per table, or shared pool?
