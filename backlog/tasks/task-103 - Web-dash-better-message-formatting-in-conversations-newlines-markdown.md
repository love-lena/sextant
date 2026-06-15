---
id: TASK-103
title: 'Web dash: better message formatting in conversations (newlines, markdown)'
status: To Do
assignee: []
created_date: '2026-06-15 16:55'
labels:
  - feature
  - dash
  - frontend
  - ux
  - 'slug:feat-dash-chat-message-formatting'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 98000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Chat messages in the web dash are rendered as plain text — newlines collapse and there's no markdown formatting. Long agent messages with structure (bullet points, code references, numbered steps) lose their formatting entirely. Should render at minimum newlines as line breaks, ideally light markdown (bold, inline code, lists).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Newlines in chat messages are rendered as line breaks in the conversation view
- [ ] #2 Basic markdown renders in chat messages: bold, inline code, bullet lists
- [ ] #3 Raw markdown syntax does not leak into the rendered output (no double-escaping)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Surfaced in outbox 2026-06-15. Related: [[feat-dash-artifact-links-in-chat]] (TASK-92)
<!-- SECTION:NOTES:END -->
