---
id: TASK-84
title: >-
  Goal progress as a bus primitive: agents update status instead of acking in
  chat
status: In Progress
assignee: []
created_date: '2026-06-13 03:52'
updated_date: '2026-06-16 01:39'
labels:
  - deferred
dependencies: []
priority: medium
ordinal: 89000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Goals panel in the dash is stubbed — no real bus primitive backs it. The goal primitive should be generic and declarative, like workflow definitions: a goal is a named, typed record with a status field and lifecycle events, not hardwired to a specific agent pattern. Agents publish goal.update events to transition status; the dash surfaces live state without cluttering conversations with ack messages. Design direction: goal defs should be reusable across contexts — similar to how sextant.workflow/v1 defines a workflow shape, a goal record defines a named intent + current status. Keep it generic enough that any agent or tool can create/update goals.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A goal record type exists (lexicon) with a status field (e.g. pending / in-progress / waiting-for-human / implementing / blocked / done)
- [ ] #2 Agents publish goal.update events to transition status; the dash Goals panel reflects live status without a page reload
- [ ] #3 An agent receiving and acking feedback transitions its goal status rather than sending a chat.message
- [ ] #4 The dash Goals panel shows real goal progress, not a stub
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Define a goal lexicon record. Agents publish to a goal subject (msg.topic.goals or per-goal subject). Dash subscribes and renders. This replaces ack messages with observable state transitions.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lena's direction 2026-06-15: goal primitive should be generic like workflow defs — not just a status tracker, but a reusable named-intent record that any agent/tool can create and update.

2026-06-15: Lena approved proposal-goal-status-lexicon. Decisions locked: per-agent status, agent.status lexicon, status.<id> artifacts (latest-value), enum idle/working/waiting-for-human/blocked/done. Orion building (1) lexicon + (2) un-stub hook on #132's base; (3) dash Crew panel follows.
<!-- SECTION:NOTES:END -->
