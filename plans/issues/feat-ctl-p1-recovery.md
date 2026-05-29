---
title:          P1 — recovery: auto-restart involuntarily-lost agents
status:         open
priority:       P1
created_at:     2026-05-29T14:55:00-07:00
labels:         [feature, control-plane, reconciler]
discovered_in:  control-plane RFC §5.3, §8
---

Add the **recovery branch** to the reconcile loop: when `desired=run ∧
observed ∈ {lost, crashed} ∧ RestartPolicy ≠ Never ∧ under crash budget`,
re-actuate (via the single-source builder). The operator-facing payoff: lost
agents self-heal instead of *sitting there*.

Add a per-agent **`RestartPolicy`** (`Always`/`OnFailure`/`Never`, default
`OnFailure`).

**Safety rails (the real work):** exponential backoff 10s ×2 cap 300s; reset
after **10 min** stable run (independent constant, not 2× cap); crash budget
**5 restarts / 10 min → terminal `crashed`** (monotonic lifetime
`restart_count` + separate windowed counter); grace SIGTERM→30s→SIGKILL;
**liveness** (3 consecutive health-check failures / 10s → restart path) to
catch a wedged-but-still-running agent `docker die` never fires on.

**Why:** RFC §1 motivation #1 — the move from tracker to self-healing.

**Acceptance:**
- **E2E (real daemon + docker):** kill an agent's container and watch it
  auto-restart and **resume from its persisted session**; crash-loop a
  container and watch the budget trip → terminal `crashed`, surfaced in
  `agents list` with a `RESTARTS` count; wedge a (still-running) agent and
  watch liveness restart it.
- **Regression:** P0's convergence still holds; a clean exit under
  `OnFailure` is **not** restarted; an intentionally `stopped`/`archived`
  agent is **not** resurrected; backoff schedule + budget→terminal are
  deterministic under an injected clock.
- **Expected breakage:** none (additive — restores the behavior P0 declared
  absent).

**Depends on:** [[feat-ctl-c0-container-spec-builder]],
[[feat-ctl-p0-reconcile-spine]]. **Sequencing:** Wave 4, first (touches the
reconcile loop + `agent.go`). Part of [[feat-control-plane-milestone]].
