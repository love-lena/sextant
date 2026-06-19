---
id: TASK-116
title: 'Bus: subscribe to all messages from a specific sender'
status: To Do
assignee: []
created_date: '2026-06-15 22:58'
labels:
  - feature
  - bus
  - subscriptions
  - 'slug:feat-bus-sender-subscription'
  - P3
  - needs-triage
dependencies: []
priority: low
ordinal: 111000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Operators want to follow all traffic from a given agent or person across all topics, not just a single subject. Today you'd need to know and subscribe to every topic they post to. A person-scoped subscription would let you say 'show me everything orion sends' and get a merged stream — useful for debugging, auditing an agent, or building a personal feed.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 sextant subscribe (or equivalent API) accepts a --sender <id> flag that delivers all messages from that ULID across any subject
- [ ] #2 works across both topic and DM subjects
- [ ] #3 the stream is ordered by bus sequence number
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
NATS subject wildcards don't model sender; likely a client-side fan-out over a broadcast index, or a dedicated sender-index subject the bus stamps on publish. Needs ADR if it changes the wire format.
<!-- SECTION:PLAN:END -->
