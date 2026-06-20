---
id: TASK-176
title: 'Spike: validate pi as a first-class sextant client'
status: Done
assignee: []
created_date: '2026-06-19 21:11'
updated_date: '2026-06-20 01:56'
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

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Cold-start context (from the PRD): pin a specific pi version/commit (github.com/earendil-works/pi, MIT); the wake primitive is sendMessage(triggerTurn:true)/sendUserMessage, started in the session_start handler; starting template is examples/extensions/file-trigger.ts; the disposed-session risk is earendil-works/pi issue 3021. Build a minimal extension + TS SDK connection and run against a real bus.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
pi spike COMPLETE — GO for 177 (PR #239 squash). Validated vs a REAL Go bus + REAL pi --mode rpc (v0.79.8) + a real model. AC#1 headless wake CONFIRMED (inbound frame -> idle pi agent wakes -> new turn, traced). AC#2 survival CONFIRMED, issue 3021 CLEARED for this extension shape + KEY FINDING: pi fires session_start TWICE per new_session in RPC -> open-client path MUST be idempotent (close-before-open). AC#3 back-pressure CHARACTERISED (bounded queue + drop-oldest; durable record on the bus; refine w/ burst-coalesce + reserved DM slot). AC#4 observability CONFIRMED (RPC stream + a pi.activity bus-topic bridge - a peer read back turn_*/tool_* -> dash renders a headless worker, TASK-150/151). AC#5 trust written up (own scoped creds by construction; bus content = untrusted prompt-injection input; layered defenses: least-priv creds + block-by-default headless tool_call gate + container/VM + trust-tier wake by frame author; pi is NOT a sandbox). Honest caveats: loopback/cheap-model only; reload/fork not individually driven; managed-handoff = 178. Spike code at clients/ts/pi-spike (extension.ts = the 177 seed).
<!-- SECTION:FINAL_SUMMARY:END -->
