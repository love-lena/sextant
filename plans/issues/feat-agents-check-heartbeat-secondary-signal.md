---
title: `sextant agents check` should consult heartbeat freshness alongside KV lifecycle
status: fixed
priority: P3
created_at: 2026-05-27T19:20-07:00
fixed_in: 5b11eaf
labels: [feature, cli, doctor, lifecycle, defense-in-depth]
discovered_in: post-lifecycle-truth manual smoke — user observed `agents check` returning verdict=healthy on an agent whose record's `updated_at` was 19 hours old; turned out the running daemon was a stale binary, but the case revealed that `agents check` is single-source-of-truth (KV) and has no defense-in-depth via the heartbeat cache the daemon already maintains
---

## Summary

`sextant agents check` currently makes its verdict purely from
`AgentStatus.Lifecycle` returned by `get_agent_status` (the KV). When
the L2/L3 layers haven't yet converged the KV (e.g., a brand-new race
window, a daemon restart that hasn't completed reconciliation, or a
buggy daemon that's silently skipping the reconciler), check returns
`verdict: healthy` with a record that's hours old.

The daemon already maintains a HeartbeatCache. A new RPC could expose
it; check could read `last_heartbeat_age` and downgrade the verdict to
`verdict: degraded` when `lifecycle=running` but heartbeat is >30s old.

## Fix shape

1. Add `get_agent_health` RPC handler (or extend `get_agent_status` with
   an optional `include_heartbeat` flag) that returns
   `{lifecycle, last_heartbeat_age, source}`.
2. Extend `runAgentCheck` (in `cmd/sextant/agents_check.go`):

   | `lifecycle` | `heartbeat_age` | verdict |
   |-------------|-----------------|---------|
   | running     | none / fresh    | healthy |
   | running     | > staleness     | degraded (KV not yet converged) |
   | lost        | any             | lost (existing path) |
   | ...         | ...             | (existing path) |

3. `degraded` remedy: `sextant daemon logs --tail 50` (the operator
   should look for why convergence is stuck — daemon downgrade, docker
   daemon flap, etc.).

## Why P3

This is a defense-in-depth signal for an edge case. The primary
convergence (L2 reconciler + L3 watcher) should make this verdict
rarely fire — but when it does, it tells the operator something subtle
went wrong with the daemon's lifecycle plumbing.

## Related

- [[feat-prompt-agent-heartbeat-staleness]] — same staleness threshold
  applied at the RPC boundary.
- [[feat-doctor-show-daemon-version]] — would have surfaced the
  underlying "you're running the wrong binary" diagnosis cleanly.
