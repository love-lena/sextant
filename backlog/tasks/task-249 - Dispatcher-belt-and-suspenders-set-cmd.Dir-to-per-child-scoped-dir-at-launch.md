---
id: TASK-249
title: 'Dispatcher belt-and-suspenders: set cmd.Dir to per-child scoped dir at launch'
status: To Do
assignee: []
created_date: '2026-06-29 21:19'
labels:
  - work-engine
  - dispatcher
  - security
  - P2
  - needs-triage
dependencies: []
priority: medium
ordinal: 235000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
From TASK-118: the pi recipe cds into the scoped workdir, but a recipe that forgets to cd would spawn unscoped. The dispatcher should set cmd.Dir to the per-child scoped dir at launch so scoping holds even if a recipe omits the cd.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A worker spawned via the dispatcher with a no-cd recipe still has its process CWD inside the per-child scoped dir. Proof: a mechanical test on launch() asserts cmd.Dir == the per-child scope for a recipe that does not cd. Flipper: mechanical. Fake-pass guard: a recipe that happens to cd itself does not exercise this — the test must use a no-cd recipe.
<!-- AC:END -->
