# sextant-shipper — component spec

## Role

Subscribe to NATS subjects, write to ClickHouse. At-least-once delivery with dedup on ClickHouse primary key.

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

At-least-once with primary-key dedup. JetStream consumer groups give us:
- Resumable position (last-acked sequence number)
- Re-delivery on failure
- Multi-consumer parallelism if needed

ClickHouse dedup via primary key on `id` (UUID per event). Re-inserts are no-ops.

## Backpressure handling

When ClickHouse is unreachable or slow:
- Local buffer at `~/.local/share/sextant/shipper-buffer/` using BoltDB or similar embedded KV
- Buffer drains as ClickHouse recovers
- Buffer-depth metric published periodically
- Hard cap on buffer size (10GB default); over cap → oldest events dropped with an audit event

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
