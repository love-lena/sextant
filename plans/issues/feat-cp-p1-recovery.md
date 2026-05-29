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
re-actuate (via the single-source builder). This is the operator-facing
payoff: lost agents stop *sitting there* and self-heal.

Add a per-agent **`RestartPolicy`** (`Always` / `OnFailure` / `Never`,
default `OnFailure`: exit 0 isn't restarted; nonzero / signal-terminated is).

**Safety rails (the real work):**
- Exponential backoff: 10s, ×2, cap 300s.
- Backoff reset after **10 min** continuous-and-stable run — an *independent*
  constant, not "2× the cap".
- Crash budget: **5 restarts in 10 min → terminal `crashed`** (stop
  auto-restarting, surface to operator). Keep a monotonic lifetime
  `restart_count` *and* a separate windowed counter.
- Grace: SIGTERM → 30s → SIGKILL (per-agent overridable; via `docker stop -t`).
- **Liveness (in P1):** a periodic health check — 3 consecutive failures /
  10s period → the same restart path — to catch a *wedged-but-still-running*
  agent (hung on a model call) that `docker die` never fires on.

**Why:** RFC §1 motivation #1 — recovery is what turns the tracker into a
self-healing control plane. Safe to automate because a lost agent has
nothing to interrupt.

**Acceptance:**
- A killed container auto-restarts; a clean exit under `OnFailure` does not.
- A crash-looper trips the budget → terminal `crashed`, surfaced, no further
  restarts.
- Backoff schedule + budget→terminal transition are deterministic under an
  injected clock.

**Depends on:** [[feat-cp-c0-container-spec-builder]] (lossless re-actuation),
[[feat-cp-p0-reconcile-spine]]. **Sequencing:** Wave 4, **first** — touches
the reconcile loop + `agent.go` (`RestartPolicy`). Part of
[[feat-control-plane-milestone]].
