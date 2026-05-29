---
title:          Archive silently leaks the per-agent volume on cleanup failure
status:         open
priority:       P2
created_at:     2026-05-29T14:55:00-07:00
labels:         [bug, control-plane]
discovered_in:  control-plane RFC §10.1
---

`archive.go` flips the record to `archived` and *then* does a best-effort
volume remove whose failure is **only logged to stderr** — so a failed
cleanup leaves a "reclaimed" record (`archived`, name released) with a
**leaked volume**, silently. This is the finalizer-shaped failure mode k8s
finalizers exist to prevent: don't call it gone until external cleanup is
confirmed.

**Fix shape:** add an intermediate **`archiving`** lifecycle state; reclaim
the volume **before** the terminal `archived` flip; the reconciler
([[feat-cp-p0-reconcile-spine]]) retries any agent stuck in `archiving`. The
name is not released until the volume is actually reclaimed.

**Acceptance:**
- A simulated volume-remove failure leaves the agent in `archiving` (not
  `archived`) and is retried by the reconciler.
- The agent name is not released until the volume is reclaimed.

**Depends on:** [[feat-cp-p0-reconcile-spine]] (reconciler retries the
`archiving` state). **Sequencing:** Wave 4 — touches the archive handler +
the reconcile loop. Part of [[feat-control-plane-milestone]].
