---
id: TASK-135
title: 'Dash: conversation view renders a seeded/backfilled message twice'
status: To Do
assignee: []
created_date: '2026-06-16 21:27'
labels:
  - bug
  - dash
  - conversations
  - 'slug:bug-dash-convo-message-double-render'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 125000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Flagged in pr-156-brief during the v0.5 reskin verify: a seeded message rendered TWICE in the conversation view. Pre-existing (the backfill/live de-dup path), NOT introduced by the reskin shell/wikilink. Likely the history-backfill and the live-SSE delivery both add the same message id without de-duping. Repro + fix the de-dup (key by message id across backfill + live merge).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A message that arrives via both history backfill and the live SSE stream renders exactly once (de-duped by id)
- [ ] #2 Repro steps documented (seed a message, open the convo, observe single render)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: pr-156-brief (v0.5 reskin stage a verify, 2026-06-16). Claimed via CAS (135). Lives in the Conversation view -> candidate to fix within Track-1 stage (d) (Conversations reskin). Pre-existing, not a reskin regression.
<!-- SECTION:NOTES:END -->
