---
id: TASK-101
title: 'Client session state: persist active subscriptions across reconnects'
status: To Do
assignee: []
created_date: '2026-06-15 02:30'
updated_date: '2026-06-19 21:42'
labels:
  - feature
  - identity
  - sdk
  - 'slug:feat-client-subscription-state-persistence'
  - P3
  - ready-for-human
dependencies: []
priority: low
ordinal: 96000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Agents lose their topic subscriptions on MCP restart — they have to re-subscribe manually every session. Explore whether the sextant context (or a client-side store) could persist subscription lists so reconnects auto-resubscribe. Open design question: is this a context concern, a client registry field, or an SDK-level behaviour.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Design decision recorded: where subscription state lives (context / registry / SDK)
- [ ] #2 Reconnecting client auto-restores its prior subscriptions, OR a documented reason why it should not
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Surfaced in outbox 2026-06-14 ('maybe use context to store state like active subscriptions'). Related: [[feat-named-agent-stable-identity]] (TASK-76)

Overlaps the shipped self-healing subscriptions (ADR-0037 / TASK-124, v0.5.0). Likely drift/overlap - verify before building.
<!-- SECTION:NOTES:END -->
