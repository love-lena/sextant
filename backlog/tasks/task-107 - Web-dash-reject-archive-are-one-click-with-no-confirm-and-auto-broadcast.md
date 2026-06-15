---
id: TASK-107
title: 'Web dash: reject/archive are one-click with no confirm, and auto-broadcast'
status: To Do
assignee: []
created_date: '2026-06-15 17:03'
labels:
  - bug
  - dash
  - frontend
  - ux
  - 'slug:bug-dash-reject-archive-confirm'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 102000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A single click on Reject or Archive flips state AND auto-posts to the companion topic. A misclick is public — the state is reversible but the broadcast isn't. Should add a confirm step or undo-toast on these consequential verbs.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Reject and Archive require a confirmation step (confirm dialog or undo-toast) before the action takes effect
- [ ] #2 The auto-post to the companion topic does not fire on accidental clicks that are immediately undone
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Found in dogfood findings artifact (vega, 2026-06-12). Finding #6.
<!-- SECTION:NOTES:END -->
