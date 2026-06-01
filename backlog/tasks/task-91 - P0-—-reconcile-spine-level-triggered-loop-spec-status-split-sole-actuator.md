---
id: TASK-91
title: 'P0 — reconcile spine: level-triggered loop, spec/status split, sole actuator'
status: To Do
assignee: []
created_date: '2026-05-29 14:55'
labels:
  - feature
  - control-plane
  - reconciler
  - 'slug:feat-ctl-p0-reconcile-spine'
  - P2
dependencies: []
priority: medium
ordinal: 91000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
**The trunk.** Replace the one-shot, edge-triggered "mark `lost`" machinery
with **one idempotent, level-triggered reconcile** that is both the **sole
writer of observed state** and the **sole actuator** (handlers write desired
state to KV; only the reconciler touches the container runtime).

Split `AgentDefinition.Lifecycle` into **`spec.desired`** (`run`/`paused`/
`archived`, operator-written) + **`status.observed`** (`running`/`crashed`/
`lost`/`ended`, plus `restart_count`/`last_exit`/`phase`/
`observed_generation`/…, reconciler-written) — RFC Appendix C has the full
record. The three sensors (L1 heartbeat, L3 docker `die`-watcher, startup
reconciler) become **hint sources that enqueue**; add a periodic full sweep
(30–60s) and a keyed, deduplicating, single-in-flight-per-agent work queue (1
worker to start).

Imperative verbs become desired-state edits (`spawn`→`desired=run`,
`stop`→`desired=stopped`, `archive`→`desired=archived`); **`restart` = bump
`spec.reactuation_nonce`** (k8s `rollout restart`-style).

**Why:** closes the open loop — self-healing, and (because level-triggered +
idempotent + single-writer) *testable*. Everything downstream is a branch in
this loop.

**Carry forward, don't re-litigate:** incarnation-ID CAS,
"sidecar-observed-terminal outranks daemon-inferred `lost`", the 5s `die`
debounce — absorb into the reconciler, don't lose.

**Acceptance:**
- **E2E (real daemon + docker):** spawn (write `desired=run`) → reconciler
  creates the container and it reaches `ready`; `stop` → reconciler stops it;
  `archive` → reconciler tears it down; kill a container out-of-band →
  reconciler marks it `lost` (no auto-restart yet — that's P1); restart →
  nonce bump → fresh incarnation.
- **Regression (port the existing lifecycle suite):** incarnation CAS still
  rejects stale envelopes; a clean sidecar terminal still outranks a
  daemon-inferred `lost`; the debounce still suppresses the race; convergence
  unit tests (inject desired + fake docker, assert action); a dropped `die`
  event still converges on the next sweep; reconcile-twice is a no-op; a
  `status` write does **not** trigger a reconcile.
- **Expected breakage (declared):**
  - `AgentDefinition` schema changes (spec/status split) → **old persisted
    records need migration**; ship a migration (or a documented one-time
    reset) — name it in the PR.
  - **Auto-recovery is still absent** (a `lost` agent stays `lost`) —
    restored by [[feat-ctl-p1-recovery]].
  - Code reading `Lifecycle` directly must move to
    `spec.desired`/`status.observed` (internal).

**Depends on:** [[feat-ctl-c0-container-spec-builder]] (the builder it
actuates), [[feat-ctl-c2-verbspec-table]] (registration). **Sequencing: Wave
3, SOLO.** Rewrites `agent.go` + all handlers + introduces the reconciler;
everything downstream waits for merge. The most stale-base-sensitive ticket.
Refs: RFC §5.1, §5.2, §5.9, Appendix C. Part of
[[feat-control-plane-milestone]].
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-ctl-p0-reconcile-spine.md
Discovered in: control-plane RFC §5.1, §5.2
Original created_at: 2026-05-29T14:55:00-07:00
<!-- SECTION:NOTES:END -->
