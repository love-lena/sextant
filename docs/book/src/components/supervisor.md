# supervisor

**Source**: `pkg/supervisor/`.

A generic, no-Docker, no-NATS subprocess supervisor. `sextantd` uses it to keep NATS, ClickHouse, and the shipper running; you can use it for anything that needs the same restart-with-backoff semantics.

## When to reach for this component

- You're adding a new supervised subprocess inside `sextantd`.
- You're tuning restart behaviour or the quarantine threshold.
- You're investigating why a subprocess didn't auto-restart.

## Public surface

| Symbol                | File:line                           | Purpose                                                 |
|-----------------------|-------------------------------------|---------------------------------------------------------|
| `Supervisor`          | `pkg/supervisor/supervisor.go`      | Main type. Construct via `New`.                         |
| `New(unit Unit)`      | `pkg/supervisor/supervisor.go`      | Validate, fill defaults, return `*Supervisor`.          |
| `(s *Supervisor) Run(ctx)` | `pkg/supervisor/supervisor.go` | Blocking loop: call `StartFn` repeatedly with backoff.  |
| `(s *Supervisor) Stop(ctx)`| `pkg/supervisor/supervisor.go` | Graceful stop; emits `EventStopped`.                    |
| `(s *Supervisor) Events()` | `pkg/supervisor/supervisor.go` | Read-only event channel.                                |
| `Process` interface   | `pkg/supervisor/supervisor.go`      | `Wait()`, `Stop(ctx)`.                                  |
| `StartFn`             | `pkg/supervisor/supervisor.go`      | Factory closure returned to caller.                     |
| `Unit`                | `pkg/supervisor/supervisor.go:78-82` | `{Name, Start StartFn, Policy}`.                        |
| `Policy`              | `pkg/supervisor/supervisor.go`      | Backoff + quarantine knobs.                             |
| `DefaultPolicy()`     | `pkg/supervisor/supervisor.go`      | Returns sextant's standard policy.                      |
| `Event`, `EventKind`  | `pkg/supervisor/supervisor.go`      | Observable transitions.                                 |

## Policy defaults

```go
DefaultPolicy() == Policy{
    InitialBackoff:    1 * time.Second,
    MaxBackoff:        5 * time.Minute,
    QuarantineAfter:   5,
    ResetAfter:        5 * time.Minute,
}
```

`InitialBackoff` doubles on each restart attempt up to `MaxBackoff`. When a unit's uptime since last start is ≥ `ResetAfter`, the failure-streak counter is *trimmed back to 1* on the next failure (`pkg/supervisor/supervisor.go:218-221`) — not zeroed. After `QuarantineAfter` consecutive failures (without that reset), the supervisor emits `EventQuarantined` and `Run` returns a wrapped error (`supervisor: <name> quarantined after N failures: …`).

In `sextantd`, these knobs are sourced from `daemon.restart_backoff_initial`, `daemon.restart_backoff_max`, and `daemon.restart_quarantine_after` in `sextantd.toml`.

## Event kinds

The `Events()` channel emits, in order:

- `EventStarted` — `StartFn` returned a process.
- `EventExited` — the process exited (cleanly or otherwise).
- `EventRestarting` — backoff before the next `StartFn` call.
- `EventQuarantined` — quarantine threshold reached; loop is ending.
- `EventStopped` — `Stop` was called.

`sextantd` has a draining goroutine that reads these and logs them; quarantine events also surface in audit.

## Why use it

The package is intentionally small and dependency-free. It doesn't know about NATS, Docker, or sextant's data model — it just knows how to start, watch, and re-start a `Process`. Everything sextant-specific (the actual subprocess command line, the event-to-log routing, the audit publish) lives in the caller.

## Test coverage

`pkg/supervisor/supervisor_test.go` covers Run/Stop round-trip, exponential backoff schedule, quarantine threshold, and the ResetAfter behaviour.
