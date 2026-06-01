---
id: TASK-85
title: Archive silently leaks the per-agent volume on cleanup failure
status: To Do
assignee: []
created_date: '2026-05-29 14:55'
labels:
  - bug
  - control-plane
  - 'slug:bug-ctl-archive-volume-leak'
  - P2
dependencies: []
priority: medium
ordinal: 85000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
`archive.go` flips the record to `archived` and *then* does a best-effort
volume remove whose failure is **only logged to stderr** — so a failed
cleanup leaves a "reclaimed" record (`archived`, name released) with a
**leaked volume**, silently. The finalizer-shaped failure mode: don't call it
gone until external cleanup is confirmed.

**Fix shape:** add an intermediate **`archiving`** lifecycle state; reclaim
the volume **before** the terminal `archived` flip; the reconciler
([[feat-ctl-p0-reconcile-spine]]) retries any agent stuck in `archiving`. The
name is not released until the volume is reclaimed.

**Acceptance:**
- **E2E (real daemon + docker):** archive an agent with a **simulated
  volume-remove failure** (inject the fault) and observe it stays `archiving`
  and is retried by the reconciler; on success it reaches `archived` and
  releases the name. Then archive a healthy agent and confirm the normal path
  reaches `archived` + reclaims the volume.
- **Regression:** normal archive still works and is idempotent (re-archiving
  an `archived` agent is a no-op); the name is reusable only after full
  reclamation.
- **Expected breakage:** the new `archiving` intermediate state is added —
  any exhaustive switch over lifecycle states must handle it (internal;
  minor).

**Depends on:** [[feat-ctl-p0-reconcile-spine]] (reconciler retries
`archiving`). **Sequencing:** Wave 4 — touches the archive handler + the
reconcile loop. Part of [[feat-control-plane-milestone]].
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/bug-ctl-archive-volume-leak.md
Discovered in: control-plane RFC §10.1
Original created_at: 2026-05-29T14:55:00-07:00
<!-- SECTION:NOTES:END -->
