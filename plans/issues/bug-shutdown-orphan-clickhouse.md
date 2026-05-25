---
title: sextantd graceful shutdown leaves clickhouse-server orphan
status: open
priority: P1
created_at: 2026-05-24T23:18-07:00
labels: [bug, supervisor, shutdown, clickhouse]
discovered_in: phase-1 smoke run (reproduced 3+ times)
---

## Summary

When sextantd receives SIGTERM, its NATS subprocess shuts down cleanly but its supervised ClickHouse subprocess outlives the daemon. The orphan keeps holding the data dir lock and TCP port until killed manually.

## Repro

1. `sextantd &` (with NATS + ClickHouse subprocesses managed by `pkg/supervisor`)
2. `kill $(pgrep sextantd)` — sends SIGTERM
3. Wait 10s for graceful shutdown
4. `pgrep -fl clickhouse` — still shows `clickhouse server -C ~/.local/share/sextant/clickhouse/config.xml` running
5. `pgrep -fl nats-server` — empty (NATS dies cleanly)
6. `kill <clickhouse-pid>` succeeds on plain SIGTERM — the ClickHouse binary itself is well-behaved

## Impact

- Subsequent `sextantd &` fails: ClickHouse already bound to its port and holding the data dir
- Operators must always manually `pgrep -f clickhouse | xargs kill` after stopping sextantd
- The orphan also accumulates over the day if multiple stops happen

## Hypothesis

Either:
- sextantd's shutdown path sends SIGTERM only to the immediate child pid, not to the process group (the leak-fix commit `2903609` addressed this for the test path via `signalProcessGroup`, but the daemon's own shutdown sequence may not use it)
- sextantd exits before ClickHouse's own shutdown drain finishes, orphaning the still-shutting-down process

## Proposed fix

Audit `pkg/sextantd`'s shutdown sequence (probably in `daemon.go` or where signal handling lives). Ensure the supervised ClickHouse Stop() goes through `signalProcessGroup` (same shape as the leak fix). Wait for `cmd.Wait()` to return before sextantd exits, with a bounded timeout that escalates to SIGKILL.

## Acceptance

`TestSextantdShutdownKillsClickHouse`: start daemon, capture clickhouse PID via `pgrep -f "config.xml"`, send SIGTERM to sextantd, assert pgrep returns no matches within `shutdown_timeout + 1s`.

## Related

- Test-orphan leak fix `2903609 fix(boot): kill the full subprocess group on Stop, not just the leader`
- Reproduced at 23:00 PT and ~22:33 PT during phase-1 verification
