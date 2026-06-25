---
id: TASK-233
title: 'Bug: LinkWorkstream ''+ link / linked'' toggle writes nothing (B.6 dead)'
status: To Do
assignee: []
created_date: '2026-06-25 03:00'
labels:
  - bug
  - dash
  - review
  - goals
  - ready-for-agent
  - 'slug:bug-linkworkstream-toggle-noop'
  - P2
dependencies: []
priority: medium
ordinal: 222000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TASK-210 (B.6 — Link a workstream to a criterion) shipped a dead control. onToggleLink (app.jsx:1387) is an empty no-op whose comment falsely claims 'relates write owned by the goals convention; reflected on reload'. The LinkWorkstream toggle (review-author.jsx:313-325) flips local 'linked' Set state only — no bus write, no relates/toward relation created — and a reload re-seeds from criterion.linked, losing the toggle. The whole overlay (reached from a goal criterion's '+ link a workstream', goals.jsx:269) is dead.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Toggling 'link' on a criterion candidate writes/removes a relates entry (read-merge-CAS on the goal artifact) that survives reload, OR the overlay path is removed until the write exists
- [ ] #2 The misleading 'reflected on reload' comment is resolved
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: capability-gap audit 2026-06-24. Verified app.jsx:1387, review-author.jsx:313-325. Undercuts [[task-210]]. The parallel goals.jsx onLinkCrit degrades honestly to a spawn; this overlay does not. Relates [[feat-goals-toward-run-bindings]].
<!-- SECTION:NOTES:END -->
