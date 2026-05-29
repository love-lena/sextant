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
label** (from [[feat-ctl-c0-container-spec-builder]]) and its `wire_epoch`
against the recomputed desired fingerprint / current `WireEpoch`; on
mismatch, **converge by restart at a turn boundary** (the sidecar emits
`lifecycle.turn_ended`) — never mid-turn.

Add `desired_generation` / `observed_generation` so the reconciler knows it
has applied the latest spec edit — this is what makes **editing a live
agent's spec** (image/env/mounts) converge. Don't overload
`current_incarnation_id` (run-identity ≠ spec-version).

**Why:** the runtime half of version-skew detection (stale sidecar after a
daemon upgrade) and the declarative "edit the record → converge" promise.

**Acceptance:**
- **E2E (real daemon + docker):** run an agent on an old image, upgrade the
  daemon/image, and watch the agent **restart onto the new image at a turn
  boundary**; edit the desired image on a running agent and watch it
  converge (with `observed_generation` catching up).
- **Regression:** a healthy, non-drifted agent is **not** restarted (no
  false positives from docker-injected mounts/env — we diff our fingerprint,
  not the live spec); an agent **mid-turn** is not interrupted; P1 recovery
  still works.
- **Expected breakage:** none.

**Depends on:** [[feat-ctl-c0-container-spec-builder]] (fingerprint),
[[feat-ctl-p0-reconcile-spine]], [[feat-ctl-p1-recovery]] (shares the loop +
re-actuation path). **Sequencing:** Wave 4, after P1. Part of
[[feat-control-plane-milestone]].
