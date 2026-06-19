---
id: TASK-117
title: >-
  Backlog: bus-artifact CAS counter for collision-free ticket numbering across
  worktrees
status: To Do
assignee: []
created_date: '2026-06-15 22:59'
labels:
  - feature
  - backlog
  - bus
  - 'slug:feat-backlog-bus-counter'
  - P3
  - needs-triage
dependencies: []
priority: low
ordinal: 112000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Filing tickets from parallel worktrees causes numbering collisions — each worktree's CLI counts from its local board and races with others. Third collision in one session today. Fix: a bus artifact (e.g. backlog.counter) that agents CAS-increment to claim the next number before creating the file. Dogfoods the bus; ends the race without a central registry.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 backlog CLI (or a thin shim) claims a ticket number by CAS-updating a bus artifact before writing the task file
- [ ] #2 two concurrent claims from different worktrees can't get the same number
- [ ] #3 falls back gracefully if the bus is unavailable (local counter + manual reconcile, same as today)
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Bus artifact backlog.counter holds {next: N}. Claim = get + CAS update to N+1; retry on conflict. Shim wraps backlog task create to do the claim first.
<!-- SECTION:PLAN:END -->
