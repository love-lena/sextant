---
title: sextantd should auto-supervise sextant-shipper (M6 deferred work)
status: resolved
priority: P3
created_at: 2026-05-24T23:18-07:00
resolved_at: 2026-05-25T00:00-07:00
labels: [feature, supervisor, shipper]
discovered_in: phase-1 smoke verification + post-wire-up
resolution: |
  Implemented via pkg/shipperboot (mirrors pkg/natsboot / pkg/clickhouseboot
  process-group lifecycle). sextantd's startup sequence spawns sextant-shipper
  after NATS+ClickHouse, passing --config and --runtime-file. Shutdown unwinds
  in reverse-dependency order (shipper → ClickHouse → NATS). New
  [shipper] auto_supervise=true (default) in sextantd.toml; auto_supervise=false
  preserves the M6 standalone-operator behavior. Binary path resolves to the
  sextantd binary's sibling first, then PATH. Specs updated in
  sextantd.md §"Startup sequence" / §"Shutdown sequence" and shipper.md
  §"Wire-up to sextantd". Tests: TestSextantdSupervisesShipper,
  TestSextantdAutoSuperviseOff, and the M11 walkthrough now asserts
  query_audit returns non-empty rows without manual shipper start.
---

## Summary

`sextant-shipper` is the NATS→ClickHouse pipeline. Per `specs/components/shipper.md`:

> "M6 ships the separate process; sextantd does **not** spawn it. The operator runs `sextant-shipper` manually (or via a separate launchd/systemd unit). Supervisor-loop wire-up is deferred to a later milestone (M7+ when the control surface lands)."

That gate has now passed (M7 shipped, M12 shipped). Wire it in.

## Impact

- `sextant audit query` returns empty until operator manually starts shipper
- Audit forensics depends on operator discipline rather than auto-supervision
- Same drift problem if shipper crashes — operator has to notice + restart

## Proposed fix

In `pkg/sextantd/daemon.go` startup sequence, after NATS + ClickHouse are healthy, spawn `sextant-shipper` as a subprocess wrapped by `pkg/supervisor` (same wrapper used for NATS and ClickHouse). Restart-on-failure with backoff; quarantine after 5 consecutive failures.

Configurable disable via `sextantd.toml`:
```toml
[shipper]
auto_supervise = true   # default; set false to run shipper standalone
```

## Acceptance

1. Default `sextantd &` boot → `pgrep sextant-shipper` finds it within 5s
2. SIGKILL the shipper → supervisor restarts it within backoff window
3. After a few RPC calls, `sextant audit query --since 1m` returns non-empty
4. `sextantd.toml` with `[shipper] auto_supervise = false` → no auto-spawn

## Related

- `specs/components/shipper.md` "Open" section ("Wire-up to sextantd's supervisor loop — deferred to M7+")
- Pattern in `pkg/supervisor` (already supervises NATS + ClickHouse)
- [[bug-shutdown-orphan-clickhouse]] (whatever shutdown fix lands there should cover shipper too)
