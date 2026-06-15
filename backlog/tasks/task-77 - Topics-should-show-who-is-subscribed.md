---
id: TASK-77
title: Topics should show who is subscribed
status: To Do
assignee: []
created_date: '2026-06-13 03:34'
updated_date: '2026-06-15 02:30'
labels:
  - P2
dependencies: []
priority: high
ordinal: 82000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The dash (and CLI) should surface which clients are currently subscribed to a topic. Useful for knowing who will see a message before sending, and for debugging missing messages. The bus tracks subscriptions via the NATS server's connz/subsz — needs an API endpoint or SDK op to expose live subscriptions per subject.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The dash shows a subscriber list (or count) per topic/DM in the conversation sidebar
- [ ] #2 The sextant CLI can query current subscribers for a subject (e.g. sextant subscribers msg.topic.helm)
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Expose subscription info via the connz/subsz NATS monitoring endpoint server-side, or track subscribe/unsubscribe events on the bus. Surface in dash conversation view + CLI.
<!-- SECTION:PLAN:END -->
