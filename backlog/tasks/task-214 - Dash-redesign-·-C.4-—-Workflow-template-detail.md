---
id: TASK-214
title: Dash redesign · C.4 — Workflow template detail
status: To Do
assignee: []
created_date: '2026-06-24 01:08'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-work-engine
dependencies:
  - TASK-213
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: low
ordinal: 204000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Reading a workflow template + its run history. Parent: EPIC C (task-200). Covers AC §9.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S9.1 shows workflow name, a WORKFLOW.md trigger line, the full spec block, and a run-history list (each run's ULID, age, status chip; live rows open the run)
- [ ] #2 S9.2 an Actions rail: Spawn work (§7, pre-pointed at a fed goal), Edit spec (§8), and a Pause/Resume triggers toggle with a paused-state note
- [ ] #3 S9.3 a Feeds-criteria section lists the goals/criteria this template feeds (DERIVED from where its runs were pointed via relates:toward, never declared on the template), or a not-linked-yet note
<!-- AC:END -->
