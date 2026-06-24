---
id: TASK-192
title: >-
  workflow coordinator crash-loops replaying a persisted un-completable
  workflow.start on restart
status: To Do
assignee: []
created_date: '2026-06-23 23:58'
labels:
  - bug
  - workflow
  - reliability
dependencies: []
priority: high
ordinal: 182000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
On restart the workflow coordinator (listen mode) re-consumes a persisted workflow.start from the stream, dispatches its step, and — when the step can't complete (e.g. no dispatcher running, so no spawn.ack within 90s) — drains and exits. launchd KeepAlive respawns it, it re-consumes the SAME message, and loops (~90s/cycle). Surfaced 2026-06-23 when a bus restart knocked the coordinator out of its idle state; a stale v0.5.3 'Live-verify workflow probe' workflow.start kept replaying. Root cause: the persisted start is redelivered to every new coordinator (not acked/expired on terminal failure). Fix options: ack/terminate a workflow.start that fails terminally so it isn't redelivered; and/or don't drain-and-exit listen mode on a single failed run. Independent of the dash epic.
<!-- SECTION:DESCRIPTION:END -->
