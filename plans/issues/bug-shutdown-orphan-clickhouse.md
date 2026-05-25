---
title: sextantd graceful shutdown leaves clickhouse-server orphan
status: resolved
priority: P1
created_at: 2026-05-24T23:18-07:00
resolved_at: 2026-05-25T00:00-07:00
labels: [bug, supervisor, shutdown, clickhouse]
discovered_in: phase-1 smoke run (reproduced 3+ times)
resolution: >
  Root cause: cmd/sextantd/main.go canceled the daemon ctx in the
  signal handler before invoking d.Shutdown(). exec.CommandContext's
  default Cancel callback (cmd.Process.Kill) SIGKILLs only the leader
  pid, which is exactly the leak vector 2903609 fixed for the
  supervisor.Stop path — ClickHouse's watchdog child stays in the
  leader's process group, so SIGKILL-to-leader-only orphans it as
  PPID=1. By the time main.go got around to d.Shutdown(), the
  supervisor's srv.Stop fast path observed waitDone already closed
  and never called signalProcessGroup. NATS dies cleanly because it
  has no children to orphan.

  Fix (three parts):
   1. cmd/sextantd/main.go: signal handler closes shutdownCh instead
      of canceling ctx. Main goroutine drives d.Shutdown() to
      completion under shutdown_timeout, then cancels ctx as final
      cleanup. The supervisor's signalProcessGroup → cmd.Wait →
      SIGKILL escalation now runs while the subprocesses are still
      alive.
   2. pkg/clickhouseboot/server.go and pkg/natsboot/server.go: set
      cmd.Cancel to signalProcessGroup(SIGKILL). Defense in depth so
      any future ctx-cancel path also kills the whole group, not
      just the leader.
   3. cmd/sextantd/shutdown_test.go: new regression
      TestSextantdShutdownKillsClickHouse — start daemon, SIGTERM,
      assert pgrep -f <data-dir>/config.xml returns zero matches
      within shutdown_timeout + 1s.
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
