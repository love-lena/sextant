---
title: Reconcile agent_definitions against container reality at daemon startup
status: resolved
priority: P2
created_at: 2026-05-27T17:30-07:00
fixed_at: 2026-05-27T17:30-07:00
labels: [feature, daemon, lifecycle, resilience]
discovered_in: agents show reported `Lifecycle: running` 12+ hours after the docker daemon died under the sidecar; the daemon trusted the KV across its own restart even when the underlying container was gone
---

## Summary

L2 of the three-layer "agent lifecycle truth" design. On `sextantd: ready`,
the daemon walks `agent_definitions`, queries containermgr for the set of
running sidecar containers on this host, and publishes a synthetic
`transition=lost` envelope for every KV record whose `(agent_uuid,
current_incarnation_id)` pair has no matching container. The existing
LifecycleWatcher writes the `lost` state to KV under its usual CAS + yield
guards.

Without this layer the daemon could come up after a crash and serve stale
`running` records indefinitely.

## Resolution

- Spec: `docs/superpowers/specs/2026-05-27-agent-lifecycle-truth-design.md` §3
- Plan: `docs/superpowers/plans/2026-05-27-agent-lifecycle-truth.md` Task 5
- Implementation: `pkg/sextantd/lifecycle_reconciler.go`
- Wired into `cmd/sextantd/daemon.go` (step 18, runs after the lifecycle
  watcher subscribes but before serving RPCs)
- Gated by `[lifecycle] reconcile_on_startup` (default true)

## Related

- [[feat-prompt-agent-heartbeat-staleness]] — L1 sibling.
- [[feat-lifecycle-container-event-watcher]] — L3 sibling, real-time.
- [[bug-agents-list-stale-lifecycle]] — the original sidecar-driven fix
  that this builds on.
