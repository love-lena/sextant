---
id: TASK-72
title: 'Message delivery status: sent / received / seen (like texts)'
status: To Do
assignee: []
created_date: '2026-06-13 02:29'
labels:
  - feature
  - protocol
  - observability
  - lexicon
  - 'slug:feat-message-delivery-status'
  - P2
  - needs-triage
dependencies: []
priority: medium
ordinal: 77000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Operators and agents need to know whether a sent message has reached the recipient's connection (received) and whether the agent itself has actually processed it (seen). In harnesses like Claude Code, a message may arrive at the client while the agent is mid-turn — it isn't delivered to the agent until the turn completes. Lena wants the sent/received/seen distinction so she can tell when an agent is genuinely unresponsive vs. just busy finishing a turn.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A sent message carries a delivery receipt back to the sender when the recipient's SDK client acknowledges the frame (received)
- [ ] #2 The SDK or harness hook emits a 'seen' signal when the message is actually delivered to the agent (e.g. the next --resume fires in Claude Code, or the handler is invoked in a long-running client)
- [ ] #3 The dash surfaces the delivery state per message — sent / received / seen — mirroring the text-message pattern
- [ ] #4 Design accounts for the harness gap: a harness that can't report seen emits only received; the absence of seen is observable (not assumed to mean seen)
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Likely needs protocol/lexicon work for the receipt record + SDK hook surface; exact shape TBD. May need per-harness adapter (Claude Code: resume hook; long-running SDK client: handler-invocation callback). Delivery receipt could be a bus-level ack or a convention on a companion subject.
<!-- SECTION:PLAN:END -->
