---
title: TestDaemonRestartsNATSAfterKill flakes when kill cascade hits template sync
status: fixed
priority: P3
created_at: 2026-05-25T17:40-07:00
fixed_in: d207cd7
labels: [bug, test, flake, supervisor, nats]
discovered_in: 2026-05-25 module-rename verification run (1 failure in 1 full-package run; 3/3 passes on isolated retry)
---

## Summary

`TestDaemonRestartsNATSAfterKill` in `cmd/sextantd/sextantd_test.go` intermittently times out waiting for NATS to come back up after a deliberate kill. The daemon log shows the restart was scheduled, but a concurrent template-sync write hits the dropped connection and the test's `waitForNATSRestart` poll never observes a new pid before the 15 s deadline.

## Repro

`go test -short -count=1 ./cmd/sextantd/` — fails ~1 / N runs under load (observed during the module-rename verification immediately after killing a large local daemon stack, so the box was busy). Isolated retry (`go test -count=3 -run TestDaemonRestartsNATSAfterKill ./cmd/sextantd/`) passes 3/3.

## Failure signature

```
sextantd_test.go:458: killed nats pid=15261
sextantd_test.go:469: waitForNATSRestart: timed out after 15s waiting for nats restart (pid still 15261)
--- daemon log ---
sextantd: nats started (try=0)
sextantd: nats exited (try=1): signal: killed
sextantd: nats restarting in 100ms (try=1)
sextantd: shipper exited (try=1): start sextant-shipper: context canceled
sextantd: shipper restarting in 100ms (try=1)
sextantd: clickhouse exited (try=1): exit status 143
sextantd: clickhouse restarting in 100ms (try=1)
sextantd: start: build spawn runtime: sync templates from .../templates: templates: put "default": nats: connection closed
```

Log truncates after the template-sync error — no `nats started (try=1)` line appears within the test's 15 s window.

## Hypothesis

Killing NATS triggers a cascade:
1. NATS exits → supervisor schedules restart in 100 ms.
2. Shipper's NATS connection drops → shipper exits → supervisor schedules its restart.
3. ClickHouse exits with status 143 (SIGTERM) — unclear why; either the cascade signal-propagates, or another goroutine takes it down.
4. Some path (looks like `build spawn runtime` / template sync) tries to write to NATS KV during the restart window and fails with `nats: connection closed`.

The fourth step is the suspect: an attempted KV write during the supervisor's restart-backoff window returns an error that may unwind a goroutine the test is implicitly waiting on, OR it just stalls long enough that the 15 s nats-up poll expires.

The flake correlates with system load — observed under contention (right after killing ~12 sextant processes during the rename). The 100 ms restart backoff plus a 15 s poll window is normally generous, but a contended box stretches NATS startup well past 100 ms.

## Proposed fix shape

Two options, not mutually exclusive:

1. **Decouple template sync from NATS readiness during restart.** The `build spawn runtime` path that calls template sync should either (a) wait for the supervisor's NATS-restart signal before issuing KV writes, or (b) retry on `nats: connection closed` rather than surfacing as a `start:` error. Audit `cmd/sextantd/spawn.go` (which the log message comes from) to confirm where this is gated.

2. **Make the test less load-sensitive.** Bump `waitForNATSRestart`'s deadline to 30 s, OR poll on a more reliable signal than pid (e.g., a successful client `Connect()` round-trip on the supervisor's reported port). Pid-based polling can also miss a fast restart if the new pid is observed before the test's `wait` loop tick.

(1) is the real fix; (2) is a band-aid that reduces flake frequency without addressing the underlying race.

## Acceptance

`TestDaemonRestartsNATSAfterKill` passes 50 / 50 runs under `go test -count=50 -run TestDaemonRestartsNATSAfterKill ./cmd/sextantd/` on a moderately loaded laptop. If (1) is taken: a new test exercises template sync during a deliberate NATS kill, asserting the operation either succeeds-after-retry or returns a typed transient error.

## Related

- `pkg/supervisor` — process group kill / restart logic (recently audited for [[bug-shutdown-orphan-clickhouse]])
- `cmd/sextantd/spawn.go` — the `build spawn runtime` path
- `pkg/templates` — template KV sync
