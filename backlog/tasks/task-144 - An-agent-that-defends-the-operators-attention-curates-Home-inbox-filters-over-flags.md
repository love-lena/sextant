---
id: TASK-144
title: >-
  An agent that defends the operator's attention (curates Home/inbox, filters
  over-flags)
status: To Do
assignee: []
created_date: '2026-06-16 23:46'
labels:
  - feature
  - agent
  - dash
  - inbox
  - design
  - 'slug:feat-attention-defending-curation-agent'
  - P2
  - ready-for-human
dependencies: []
priority: medium
ordinal: 134000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena (2026-06-16, press-release review): 'human time and attention is actively defended' — she wants an AGENT defending her time by managing her inbox/home. The problem: agents keep marking artifacts ready-for-review when they shouldn't be, so her inbox gets noise. The fix: an agent that PROACTIVELY curates the operator's Home + inbox — re-judges what agents flag needs-review against what genuinely needs the operator, keeps the over-eager flags off her plate, surfaces only the real calls. The goals-design D6 agent-curation layer made real (projection -> agent-curation -> human). May be the SAME agent as the Assistant (TASK-138) — 'your agent' that defends proactively AND answers when messaged; design call whether they unify. v0.5.0 scope.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 An agent curates the operator's Home/inbox — surfaces only what genuinely needs them, not every agent-flagged needs-review
- [ ] #2 Over-flagged 'ready for review' artifacts are filtered/down-ranked, not shown as if they need the operator
- [ ] #3 The curation is observable + correctable (operator + owner stay authoritative; signal-not-manage)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lena (press-release review, 2026-06-16). The D6 projection->curation->human layer made real. May unify with TASK-138 (the Assistant = your agent). Also tighten the needs-review convention (agents over-flag). Design pass needed (gate like the goals model). Claimed via backlog.counter CAS (144). v0.5.0 scope.
<!-- SECTION:NOTES:END -->
