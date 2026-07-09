---
id: TASK-303.1
title: Prove and choose session A's wake mechanism
status: To Do
assignee: []
created_date: '2026-07-09 20:11'
labels:
  - 'wayfinder:prototype'
dependencies: []
parent_task_id: TASK-303
ordinal: 233000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Question
Can a manned Claude Code session A, after publishing a plan artifact and subscribing, be RELIABLY woken by Lena's dash review/comment and resume to act with nobody typing in its terminal? Choose the mechanism and PROVE it on the devbox: channel push (sextant-mcp notifications/claude/channel; needs --dangerously-load-development-channels; the content-less WAKE_ONLY path is an unproven spike, TASK-57) vs. a background 'sextant subscribe' / message_read Monitor poll as fallback. KEYSTONE: if this is not clean, the loop's shape changes.
<!-- SECTION:DESCRIPTION:END -->
