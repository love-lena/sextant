---
id: TASK-82
title: 'Web dash: conversation unread + participant counts'
status: To Do
assignee: []
created_date: '2026-06-13 03:34'
labels:
  - feature
  - dash
  - frontend
  - 'slug:feat-dash-conversation-unread'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 87000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The D2 conversation list shows last-activity but unread + participant counts are stubbed at 0 (no read-state / roster source). Wire real unread (vs a last-seen cursor the dash tracks) + participant counts where derivable.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 each conversation shows a real unread count from a tracked last-seen cursor
- [ ] #2 topics/DMs show a participant count where derivable
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Follow-up from D2 [[feat-dash-web-ui-d2]] (TASK-71). Deferred in conversation-depth (#3). May overlap [[feat-message-delivery-status]] (TASK-72).
<!-- SECTION:NOTES:END -->
