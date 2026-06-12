---
id: TASK-57
title: Wake-only channel + channel-validate + Monitor fallback
status: Done
assignee: []
created_date: '2026-06-12 00:04'
updated_date: '2026-06-12 02:40'
labels:
  - feature
  - principal-trust
  - mcp
  - sextant-mcp
  - channel
  - 'slug:feat-wake-only-channel'
  - P2
  - ready-for-agent
dependencies:
  - TASK-56
priority: medium
ordinal: 63000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Make the channel a WAKE signal, not a content path: the sextant MCP channel push goes notification-only ('new sextant traffic'), and the auth hook delivers the records — so the agent never receives a wrapped copy of a validated message. SHIP wake-only IF technically possible (spike: can the channel push be notification-only AND still wake an idle session); otherwise fall back to the channel still pushing content and the agent disregarding the wrapped copy, trusting the hook's signed one. Also: on startup the skill/MCP VALIDATES channels are enabled (the subscribed-notice check) and, if not, stands up an explicit Monitor (sextant subscribe in the background) as the wake/pickup fallback (per the channels docs).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Spike recorded: whether the MCP channel push can be notification-only and still wake an idle session
- [x] #2 When possible, a validated principal message produces a wake via the channel and is delivered (content) only by the hook — no wrapped content copy reaches the agent
- [x] #3 If wake-only is not possible, the documented fallback (content channel + agent disregards the wrapped copy) is in place and noted
- [x] #4 On startup the client verifies channels are enabled (subscribed-notice); if not, it stands up a Monitor (sextant subscribe) as the wake/pickup fallback
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Spike wake-only feasibility first. If feasible, change the MCP channel push to notification-only. Add the channels-enabled check + Monitor fallback per the channels docs. Coordinate with TASK-52 (self-echo), which touches the same delivery path.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented + verified on branch task-53-principal-trust (PR #109): 7b1f1bf (+ DM wake bridge 6d554c6). gofumpt/vet + go test -race + e2e all green. Adversarial review: no Critical; trust model proven sound. Rides TASK-53 for human sign-off.
<!-- SECTION:NOTES:END -->
