---
title:          P0 — reconcile spine: level-triggered loop, spec/status split, sole actuator
status:         open
priority:       P2
created_at:     2026-05-29T14:55:00-07:00
labels:         [feature, control-plane, reconciler]
discovered_in:  control-plane RFC §5.1, §5.2
---

**The trunk.** Replace the one-shot, edge-triggered "mark `lost`" machinery
with **one idempotent, level-triggered reconcile** that is both the **sole
writer of observed state** and the **sole actuator** (handlers write desired
state to KV; only the reconciler touches the container runtime).

Split `AgentDefinition.Lifecycle` into **`spec.desired`** (`run` / `paused` /
`archived`, operator-written) + **`status.observed`** (`running` / `crashed`
/ `lost` / `ended`, plus `restart_count` / `last_exit` / `phase` /
`observed_generation` / …, reconciler-written) — see RFC Appendix C for the
full record. The three sensors (L1 heartbeat, L3 docker `die`-watcher,
startup reconciler) become **hint sources that enqueue**; add a periodic
full sweep (30–60s) and a keyed, deduplicating, single-in-flight-per-agent
work queue (1 worker to start).

Imperative verbs become desired-state edits: `spawn`→`desired=run`,
`stop`→`desired=stopped`, `archive`→`desired=archived`; **`restart` = bump
`spec.reactuation_nonce`** (k8s `rollout restart`-style; the reconciler
re-actuates when `observed_nonce < reactuation_nonce`).

**Why:** closes the open loop — the system becomes self-healing and, because
the loop is level-triggered + idempotent + single-writer, *testable*
(convergence is a unit test; a dropped event is recoverable). Single-writer
eliminates the in-process multi-writer race across the three sensors.
Everything downstream is a branch in this loop.

**Carry forward, don't re-litigate:** the existing 3-layer lifecycle's
race-hardening — incarnation-ID CAS, "sidecar-observed terminal outranks
daemon-inferred `lost`", the 5s `die` debounce — must be *absorbed into* the
reconciler, not lost.

**Acceptance:**
- Convergence unit tests: inject `desired` + a fake docker state, run
  `reconcile` once, assert the action — no real containers, no wall-clock.
- A dropped `die` event still converges on the next periodic sweep.
- Idempotency: running `reconcile` twice is a no-op the second time.
- No handler calls `Containers.Run/Stop`; a `status` write does **not**
  trigger a reconcile.

**Depends on:** [[feat-cp-c0-container-spec-builder]] (the builder it
actuates), [[feat-cp-c2-verbspec-table]] (registration). **Sequencing: Wave
3, SOLO.** Rewrites `agent.go` + all handlers + introduces the reconciler —
nothing parallel touches those files, and **everything downstream waits for
this to merge**. The most stale-base-sensitive ticket in the milestone.
Refs: RFC §5.1, §5.2, §5.9, Appendix C. Part of
[[feat-control-plane-milestone]].
