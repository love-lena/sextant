---
id: TASK-136
title: 'Dash: compose-box draft should be per-conversation, not global'
status: To Do
assignee: []
created_date: '2026-06-16 21:27'
labels:
  - bug
  - dash
  - conversations
  - ux
  - 'slug:bug-dash-compose-draft-per-conversation'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 126000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena (#outbox 2026-06-16): draft text typed in a compose box follows the user to whatever conversation they open, instead of sticking with that input. Root cause: the dash holds a single global 'draft' state (one setDraft) shared across all conversations/inputs. Fix: key the draft per conversation/input (e.g. draftByConvo[convoId]) so switching conversations preserves each one's unfinished draft and shows the right one.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Typing a draft in conversation A then switching to B shows B's own (empty or prior) draft, not A's
- [ ] #2 Returning to A restores A's unsent draft
- [ ] #3 Sending clears only that conversation's draft
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: #outbox (2026-06-16). Claimed via CAS (136). Lives in the compose box / Conversation view -> candidate to fix within Track-1 stage (d). The current single 'draft' state in app.jsx is the cause.
<!-- SECTION:NOTES:END -->
