---
id: TASK-104
title: 'Web dash: compose input should wrap to new lines, not expand right'
status: To Do
assignee: []
created_date: '2026-06-15 17:03'
labels:
  - feature
  - dash
  - frontend
  - ux
  - 'slug:feat-dash-compose-input-wrap'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 99000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The message compose input in conversations expands horizontally as text is typed, instead of wrapping to new lines. Long messages push the input off-screen. Should behave like a standard textarea: fixed width, grows vertically as content is added.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The compose input wraps text at the container edge rather than expanding horizontally
- [ ] #2 The input grows vertically to show the full message as the user types
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Surfaced by lena 2026-06-15. Related: [[feat-dash-chat-message-formatting]] (TASK-93)
<!-- SECTION:NOTES:END -->
