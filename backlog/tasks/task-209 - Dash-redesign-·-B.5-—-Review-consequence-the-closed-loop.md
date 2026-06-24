---
id: TASK-209
title: Dash redesign · B.5 — Review consequence (the closed loop)
status: To Do
assignee: []
created_date: '2026-06-24 01:08'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-review
dependencies:
  - TASK-208
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: high
ordinal: 199000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Every verdict produces a clear, honest consequence screen so the operator sees the loop close. Parent: EPIC B (task-199). Covers AC §15.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S15.1 Approve/Answers on a spawned-run checkpoint: the run continues, resumes remaining steps, ends by writing the stopping brief; returns only at another checkpoint
- [ ] #2 S15.2 Approve/Answers on a criterion-linked brief: the criterion advances to met, the goal rollup moves, the run resumes; a monospace line states the exact transition
- [ ] #3 S15.3 Request revisions: run revises and returns to the inbox as a new version; nothing marked met
- [ ] #4 S15.4 Reject: run drops the direction, criterion does not advance. Ignore: set aside, criterion unchanged
- [ ] #5 S15.5 when a criterion advanced, offer See-the-goal alongside Back-to-origin; consequence copy must match the verdict and whether a run resumes
<!-- AC:END -->
