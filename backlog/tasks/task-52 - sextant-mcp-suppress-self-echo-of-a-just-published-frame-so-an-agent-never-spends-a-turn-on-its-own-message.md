---
id: TASK-52
title: >-
  sextant-mcp: suppress self-echo of a just-published frame so an agent never
  spends a turn on its own message
status: Done
assignee: []
created_date: '2026-06-11 19:59'
updated_date: '2026-06-12 02:40'
labels:
  - bug
  - mcp
  - sextant-mcp
  - channel
  - 'slug:bug-mcp-self-echo-wastes-turn'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 58000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
When a client publishes via message_publish to a subject it is also subscribed to, the bus relays the frame back and the MCP channel delivers it as a <channel> event — which wakes a turn so the agent processes its OWN message (wrapped as untrusted input, no less). Observed repeatedly in the principal-trust experiment (2026-06-11): an agent's own publishes to msg.topic.task echoed back and each cost a turn (the agent kept having to say 'not acting on my own echo'). Any time an agent participates in a topic it is subscribed to — the normal collaboration path — it burns a turn (and tokens) on its own traffic. Fix shape: on message_publish, wait for the bus to confirm the publish and record the published frame id in a bounded per-process set; in the channel delivery path, drop a frame whose id is in that set (self-echo) so it is never surfaced to this session. Suppression is self-only (other subscribers still receive the frame) and id-based (not author-based).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Publishing to a subject this session is subscribed to does NOT deliver the just-published frame back to the same session as a channel event
- [x] #2 The same frame is still delivered normally to OTHER subscribers (suppression is self-only)
- [x] #3 message_publish waits for the bus to acknowledge the publish (so the frame id is known) before returning its result
- [x] #4 The suppression set is bounded (recent published frame ids, e.g. a ring/LRU) and does not grow without limit over a long-lived session
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
In cmd/sextant-mcp: capture the frame id returned by each message_publish into a bounded set scoped to this server process; in the channel/notification delivery path, skip emitting a <channel> event for any frame whose id is in that set. Keep it id-based (a resumed or co-identity session must still see frames it did not itself publish). message_publish should be synchronous on the bus ack so the id is available to record.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented + verified on branch task-53-principal-trust (PR #109): b7c99f5. gofumpt/vet + go test -race + e2e all green. Adversarial review: no Critical; trust model proven sound. Rides TASK-53 for human sign-off.
<!-- SECTION:NOTES:END -->
