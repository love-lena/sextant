---
id: TASK-160
title: >-
  Dash: unified 'replies to you' view — see responses to your messages in one
  place
status: To Do
assignee: []
created_date: '2026-06-17 22:36'
labels:
  - feature
  - dash
  - attention-management
  - violet
  - 'slug:feat-dash-replies-to-you'
  - P1
  - ready-for-agent
dependencies: []
priority: high
ordinal: 150000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The operator posts questions/messages across many topics (artifact threads, DMs) and has no single place to see the replies — she literally asked 'how would I see your response?' after a question on an artifact topic sat unanswered, then got answered somewhere she'd never look. This is the human-attention-management gap. NEAR-TERM dash fix ahead of the full violet SDK (every-message-answered AC on goal.violet): aggregate every thread the operator has posted in + surface ones with newer replies as a 'replies to you' rail, so a response is never lost. Subsumed by violet's curation once that ships; interim relief.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Dash surfaces a 'replies to you' view listing every topic/thread the operator has posted in that has a message newer than her last post
- [ ] #2 Each entry links to the thread + shows the latest reply preview + who replied
- [ ] #3 An unanswered operator message (no reply yet) is visibly distinct from one that's been answered
- [ ] #4 The view updates live as replies land (SSE/live-bus), consistent with the rest of the dash
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Index operator-authored messages across msg.topic.* + the DM subjects; for each, find the latest message in that thread; if newer than her last post, list it. Render as a Home rail or dedicated view. Reuse existing live-bus/SSE plumbing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Surfaced by Lena 2026-06-17 live ('how would I see your response? this is why we need better human attention management'). Interim relief ahead of goal.violet (every-message-answered AC). Related: feat-violet-sdk-client, TASK-154 (goal discussion thread inline). Discovered in: leaf-validation-result topic Q sat unanswered.
<!-- SECTION:NOTES:END -->
