---
title: Watch docker container events and publish `lost` on unobserved die
status: resolved
priority: P2
created_at: 2026-05-27T17:30-07:00
fixed_at: 2026-05-27T17:30-07:00
labels: [feature, daemon, lifecycle, resilience]
discovered_in: same agents-show-stale incident as [[feat-lifecycle-startup-reconciler]]; the startup reconciler closes the daemon-restart gap but not the live "container dies but sidecar didn't publish a terminal" gap
---

## Summary

L3 of the three-layer "agent lifecycle truth" design. The daemon
subscribes to docker container events filtered by the
`sextant.agent_uuid` label. On a `die` event for a sextant-labeled
container, the watcher starts a debounce timer (default 5s). If a sidecar
publishes a terminal lifecycle envelope (`ended` or `crashed`) for the
same incarnation during the window, the timer is cancelled. Otherwise it
publishes a synthetic `transition=lost` envelope on
`agents.<uuid>.lifecycle`.

The race between a debounce-elapsed `lost` and a slightly-later sidecar
`ended`/`crashed` is closed by the watcher's `watcherShouldYield` rule
(sidecar-observed terminals outrank daemon-inferred absence).

## Resolution

- Spec: `docs/superpowers/specs/2026-05-27-agent-lifecycle-truth-design.md` §4
- Plan: `docs/superpowers/plans/2026-05-27-agent-lifecycle-truth.md` Task 7
- Implementation: `pkg/sextantd/container_watcher.go`
- Docker SDK wrapper: `pkg/containermgr/containermgr.go::Manager.Events`
- Wired into `cmd/sextantd/daemon.go` (step 17, runs as a goroutine for
  the lifetime of the daemon)
- Threshold knob: `[lifecycle] container_watcher_debounce` (default 5s)

## Related

- [[feat-prompt-agent-heartbeat-staleness]] — L1 sibling.
- [[feat-lifecycle-startup-reconciler]] — L2 sibling, one-shot.
