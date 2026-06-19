---
id: TASK-176
title: 'Spike: validate pi as a first-class sextant client'
status: To Do
assignee: []
created_date: '2026-06-19 21:11'
labels:
  - spike
  - research
  - pi
  - 'slug:spike-pi-headless-client'
  - P2
  - ready-for-agent
dependencies:
  - TASK-174
ordinal: 166000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Spike (AFK): validate that pi can host a first-class sextant client before committing the pi-bus design. Build a minimal pi extension that opens the TS SDK at session_start and bridges an inbound bus frame to the agent loop via sendMessage with triggerTurn. Run against a real bus and write up findings. Findings + the trust-posture question feed the go/no-go on the pi-bus extension. PRD doc-2.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 headless wake confirmed end-to-end in RPC/extension mode (an inbound frame wakes an idle pi agent), or the gap is documented
- [ ] #2 connection survival tested across session_start reasons reload/fork/resume; the disposed-session risk (issue 3021) reproduced or cleared
- [ ] #3 back-pressure on a busy topic characterised; a buffering/ack policy proposed
- [ ] #4 agent-action observability confirmed: the RPC event stream (tool calls, thinking, turn events) is consumable and bridgeable to a bus activity topic
- [ ] #5 the security/trust posture is written up as a decision for the pi-bus extension (bus-delivered instructions vs pi permission gates; agent acts on its own scoped creds)
<!-- AC:END -->
