---
title:          P2 — drift: detect stale specs, converge by restart
status:         open
priority:       P2
created_at:     2026-05-29T14:55:00-07:00
labels:         [feature, control-plane, reconciler]
discovered_in:  control-plane RFC §5.6
---

Second reconcile branch: detect a container running the **wrong spec** and
converge it. Compare the running container's stamped **spec-fingerprint
label** (from [[feat-cp-c0-container-spec-builder]]) and its `wire_epoch`
against the recomputed desired fingerprint / current `WireEpoch`; on
mismatch, **converge by restart at a turn boundary** (the sidecar already
emits `lifecycle.turn_ended`) — never mid-turn.

Add the `desired_generation` / `observed_generation` pair so the reconciler
knows whether it has applied the latest spec edit — this is what makes
**editing a live agent's spec** (image/env/mounts) converge, completing the
declarative promise. Do **not** overload `current_incarnation_id` for it
(run-identity ≠ spec-version).

**Why:** the runtime half of version-skew detection — catches a stale-sidecar
agent after a daemon upgrade, and a drifted spec, without a false-positive
on docker-normalized fields (we diff a fingerprint we control, not the live
spec).

**Acceptance:**
- A container built from a now-stale spec is detected and re-actuated.
- A healthy agent mid-turn is **not** interrupted — re-actuation waits for
  `turn_ended`.
- Editing the desired image converges the agent; `observed_generation`
  catches up.
- No false-positive restarts from docker-injected mounts/env.

**Depends on:** [[feat-cp-c0-container-spec-builder]] (fingerprint),
[[feat-cp-p0-reconcile-spine]], [[feat-cp-p1-recovery]] (shares the loop +
the re-actuation path). **Sequencing:** Wave 4, after P1. Part of
[[feat-control-plane-milestone]].
