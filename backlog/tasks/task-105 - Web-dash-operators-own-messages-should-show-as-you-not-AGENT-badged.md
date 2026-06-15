---
id: TASK-105
title: 'Web dash: operator''s own messages should show as ''you'', not AGENT-badged'
status: To Do
assignee: []
created_date: '2026-06-15 17:03'
labels:
  - bug
  - dash
  - frontend
  - ux
  - 'slug:bug-dash-operator-self-badge'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 100000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Sent messages render identically to other agents and are badged 'AGENT'. Root cause: the self-enroll kind is 'human', but the filter treats it as an agent. Also puts the operator in AGENT STATUS. Should distinguish own messages as self and treat the 'human' kind like 'client' (no AGENT badge).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The operator's own messages are visually distinct (e.g. right-aligned or labelled 'you')
- [ ] #2 The 'human' kind client does not receive an AGENT badge
- [ ] #3 The operator is not counted in AGENT STATUS
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Found in dogfood findings artifact (vega, 2026-06-12). Finding #1.
<!-- SECTION:NOTES:END -->
