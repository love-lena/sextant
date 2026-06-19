---
id: TASK-150
title: 'Agent status: emit ''thinking'' at turn start (read-receipt indicator)'
status: To Do
assignee: []
created_date: '2026-06-17 18:18'
labels:
  - feature
  - dash
  - agent-status
  - 'slug:feat-agent-status-thinking-on-turn-start'
  - P3
  - needs-triage
dependencies: []
priority: low
ordinal: 140000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Today an agent's status (agent.status — the per-agent self-report via the PostToolUse Haiku hook, #132) only updates AFTER the agent takes a tool action, and it's throttled. So when Lena messages an agent there's a visible lag before the dash reflects that the agent received the message and is working. Lena's ask (outbox, 2026-06-17): update agent status as soon as the agent STARTS its turn, to immediately show 'message received, agent is thinking about it' — essentially a fancy read indicator. Makes the dash feel responsive: you see the agent pick up your message instantly, not after its first tool call.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 On turn start (e.g. a UserPromptSubmit hook or first-activity signal), agent.status flips to a 'thinking'/'received' state immediately, before any tool use
- [ ] #2 The thinking state is visually distinct in the dash Agents panel (read-receipt-style), and transitions to the normal working/idle status as the turn proceeds + the throttled Haiku summary refines it
- [ ] #3 No added latency to normal operation; the early write is cheap and does not wait on the Haiku summary
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Add a turn-start status write (UserPromptSubmit hook or equivalent) setting agent.status to a lightweight 'thinking' state; the existing PostToolUse Haiku hook continues to refine it. Pick/extend the status enum value; ensure the dash renders 'thinking'.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: Lena outbox idea 2026-06-17. Builds on the per-agent status hook (#132). Related: [[feat-agent-status-waiting-human-vs-agent]] (the status-enum work).
<!-- SECTION:NOTES:END -->
