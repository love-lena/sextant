# shipper

**Source**: `pkg/shipper/`, `pkg/shipperboot/`, `cmd/sextant-shipper/`.

The shipper is a long-running process that subscribes to NATS streams, decodes envelopes, and writes rows to ClickHouse. It's the only thing that talks to both NATS *and* ClickHouse in the steady state.

## When to reach for this component

- "I see envelopes on the bus but they're not in ClickHouse" — start here.
- You want to add a new stream-to-table mapping.
- You're tuning the local buffer or batch sizing.
- You need to know what happens when ClickHouse is unreachable.

## Process model

The shipper is a *separate process* (`cmd/sextant-shipper/main.go`) supervised by `sextantd` (via `pkg/shipperboot`). It's a separate process for failure isolation: a shipper crash doesn't take down the daemon.

You can disable the in-daemon supervision by setting `shipper.auto_supervise = false` in `sextantd.toml`. In that case, run `sextant-shipper` standalone with `--config <path> --runtime-file <path>`.

## Configuration

`~/.config/sextant/shipper.toml`:

```toml
[nats]
url            = ""        # empty → read from runtime.json
operator_creds = "~/.config/sextant/operator.creds"

[clickhouse]
addr           = ""        # empty → read from runtime.json
database       = "sextant"
user           = "sextant"
password_file  = "~/.config/sextant/clickhouse.password"

[buffer]
dir            = "~/.local/share/sextant/shipper-buffer"
hard_cap_bytes = 10737418240   # 10 GiB

[batch]
max_events     = 1000
flush_interval = "100ms"
ack_wait       = "30s"

[shipper]
degraded_mode    = ""           # "" (fail-closed) or "drop_oldest"
metrics_interval = "5s"
service_name     = "sextant-shipper"
host_id          = ""           # empty → hostname
```

## What it subscribes to and where it writes

| Durable consumer       | Subject pattern          | ClickHouse table     |
|------------------------|--------------------------|----------------------|
| `shipper-agent_frames`     | `agents.*.frames`        | `events`             |
| `shipper-agent_lifecycle`  | `agents.*.lifecycle`     | `events`             |
| `shipper-audit`            | `audit.*`                | `audit`              |
| `shipper-telemetry_traces` | `telemetry.traces.>`     | `telemetry_traces`   |
| `shipper-telemetry_metrics`| `telemetry.metrics.>`    | `telemetry_metrics`  |
| `shipper-telemetry_logs`   | `telemetry.logs.>`       | `telemetry_logs`     |
| `shipper-user_input`       | `user_input.>`           | `events`             |

Subject-to-table routing lives in `pkg/shipper/mapping.go`; row decoders in `pkg/shipper/tables.go`.

## At-least-once delivery

The `events` ClickHouse table is `ReplacingMergeTree` keyed on envelope `id`; duplicate inserts get collapsed by ClickHouse's eventual-consistency merge. Other tables (`audit`, `telemetry_*`) use plain `MergeTree`, so duplicates there persist as separate rows — at-most-once expectations on those tables aren't strict. The shipper only ACKs a JetStream message *after* the row is durable (either in ClickHouse or in the local BoltDB buffer). If the shipper crashes between processing and ACK, the next start re-reads the message and JetStream redelivery + ClickHouse `id` deduplication absorb the replay where applicable.

## Buffer behaviour

When ClickHouse is unreachable, decoded rows spill to a BoltDB file at `buffer.dir/buffer.db` with monotonic-key FIFO ordering. A drain goroutine retries rows when ClickHouse returns. The buffer is shared per-table (one bucket per ClickHouse table).

Two failure modes when the buffer fills:

| `degraded_mode`     | Hard-cap behaviour                                               |
|---------------------|------------------------------------------------------------------|
| `""` (default)      | Emit `audit.shipper_backpressure`, exit non-zero. Fail-closed.   |
| `"drop_oldest"`     | Evict the oldest buffered rows to make room. Data loss possible. |

Fail-closed is the safe default; `drop_oldest` is an explicit operator decision for "I'd rather lose old data than block the bus."

## Self-metrics

Every `metrics_interval` (default 5s), the shipper publishes a `telemetry.metrics.shipper.<host_id>` envelope with: shipper lag (per consumer), buffer depth (per bucket), write rate, and error counters. These flow into the `telemetry_metrics` table like any other metric.

## Test coverage

- `pkg/shipper/shipper_test.go` — end-to-end roundtrip against a real NATS + ClickHouse.
- `pkg/shipper/mapping_test.go` — subject routing.
- `pkg/shipper/spillover_test.go` — BoltDB FIFO drain.
- `pkg/shipper/backpressure_test.go` — hard-cap fail-closed exit.

## `pkg/shipperboot`

Wrapper that lets `sextantd` spawn and supervise the shipper as a subprocess. Resolves the `sextant-shipper` binary from `sextantd`'s sibling directory, then falls back to `PATH`. Wires SIGTERM cleanly. See `pkg/shipperboot/server.go`.
