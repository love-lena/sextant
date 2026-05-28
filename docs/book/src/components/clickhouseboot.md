# clickhouseboot

**Source**: `pkg/clickhouseboot/`, `cmd/sextant-clickhouseboot/`.

`clickhouseboot` starts a single-host `clickhouse-server` subprocess and applies the schema migrations embedded in the binary.

## When to reach for this component

- You want to know what tables ClickHouse holds in a sextant install.
- You're adding a column (write a new migration file under `pkg/clickhouseboot/migrations/`).
- You're investigating a "table not found" failure when running `sextant audit list` or `sextant traces show`.

## Public surface

| Symbol                  | File                                  | Purpose                                                 |
|-------------------------|---------------------------------------|---------------------------------------------------------|
| `Server`                | `pkg/clickhouseboot/server.go`        | Subprocess handle.                                      |
| `NewServer(cfg Config)` | `pkg/clickhouseboot/server.go`        | Construct.                                              |
| `Config`                | `pkg/clickhouseboot/config.go`        | Listen host, ports, database, user, password file.      |
| `DefaultConfig(dataDir)`| `pkg/clickhouseboot/config.go`        | Factory.                                                |
| `LoadMigrations()`      | `pkg/clickhouseboot/migrate.go`       | Return embedded migrations (NNN-name.sql files).        |
| `Apply(ctx, conn)`      | `pkg/clickhouseboot/migrate.go`       | Run pending migrations idempotently (SHA256 tracked).   |

## Tables

The exact set lives in `pkg/clickhouseboot/migrations/`. At this snapshot the migrations create:

- `events` — `ReplacingMergeTree` keyed on envelope `id`; the canonical "everything happened" table that `query_history` reads from. Replay-safe.
- `audit` — `MergeTree`; one row per auth-relevant action, retained indefinitely. (Not a ReplacingMergeTree — duplicate replays would persist as separate rows; in practice the shipper's ACK-after-durable discipline keeps this rare.)
- `telemetry_traces`, `telemetry_metrics`, `telemetry_logs` — OTel-shaped tables matching the OpenTelemetry ClickHouse exporter schema.
- `agent_definitions_history` — one row per definition mutation, so `sextant agents show --history` can paint the timeline.
- `sextant_migrations` — bookkeeping for `Apply`. Tracks the SHA256 of each applied file so re-running is a no-op.

## Migration discipline

Migration files are numbered (`001-init.sql`, `002-add-foo.sql`, ...) and applied in order. `Apply`:

1. Ensures `sextant_migrations` exists.
2. Reads each embedded file in order.
3. For each: if `(version, sha256)` is already in `sextant_migrations`, skip.
4. Otherwise: execute the SQL, then insert the bookkeeping row.

A SHA256 mismatch for an already-applied migration is treated as an error — modifying a checked-in migration is not allowed.

## Subprocess details

`Server.Start` execs `clickhouse-server --config-file <generated-config>` and waits for the "Ready for connections" log line. The generated config sets:

- Native TCP listener on `listen_host:tcp_port`.
- HTTP listener on `listen_host:http_port`.
- Data directory under the configured `data_dir`.
- Logger redirecting to `log_file` (or `/dev/null` if empty).

The password is read from `password_file` to keep it out of process listings (`sextantd.toml` defaults to `~/.config/sextant/clickhouse.password`).

## Standalone binary

`cmd/sextant-clickhouseboot/` lets you bring up a sextant-shaped ClickHouse instance without the full daemon:

```bash
sextant-clickhouseboot --data-dir /tmp/clickhouse --tcp-port 9000 --http-port 8123
```

## Test coverage

`pkg/clickhouseboot/clickhouseboot_test.go` does a full Start → Apply → INSERT → SELECT → Stop. A leak test asserts no leftover `clickhouse-server` subprocesses after teardown.
