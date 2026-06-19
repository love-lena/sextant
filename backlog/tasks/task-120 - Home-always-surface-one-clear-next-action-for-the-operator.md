---
id: TASK-120
title: 'Home: always surface one clear next action for the operator'
status: To Do
assignee: []
created_date: '2026-06-15 23:18'
labels:
  - feature
  - dash
  - home
  - ux
  - 'slug:feat-home-single-next-action'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 113000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Home artifact should always have exactly one prioritized item Lena can act on immediately — a PR to approve, a design decision to call, a gate to pass. Today it shows a list, which buries the top item. The contract: if there is anything blocked on the operator, the first agenda item is the single most important unblocking action. No item = a deliberate 'all clear' state.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Home agenda always has at least one item when anything is blocked on the operator
- [ ] #2 The first item is the single most actionable/highest-priority unblock (not a list of equals)
- [ ] #3 When nothing is blocked, Home shows an explicit 'all clear' state rather than an empty section
<!-- AC:END -->
