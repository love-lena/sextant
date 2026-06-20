---
id: TASK-184
title: 'One-command live demo: a pi agent on the operator''s bus, end to end'
status: In Progress
assignee: []
created_date: '2026-06-19 21:31'
updated_date: '2026-06-20 04:18'
labels:
  - feature
  - demo
  - pi
  - acceptance
  - 'slug:feat-pi-live-demo'
  - P2
  - ready-for-agent
dependencies:
  - TASK-177
ordinal: 174000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A self-validating one-command demo (demo.sh or a /slash-command skill the operator invokes) that closes the loop on the co-equality + pi-client claims: boot a throwaway bus, start a pi agent as its own scoped identity, the operator DMs it, it wakes and replies, sets a goal that appears, and streams its tool-calls/thinking to the activity topic rendered live in the dash. The forcing function that proves the UX landed on the operator's machine, not just in code. PRD doc-2.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 one command boots a throwaway bus + a pi agent as a distinct registered identity (not the operator's creds)
- [ ] #2 the operator DMs the pi agent and sees it wake, reply, and set a goal that renders in the dash
- [ ] #3 the agent's tool-calls/thinking stream to a bus activity topic and render live in the dash
- [ ] #4 the demo is self-validating (prints PASS/FAIL) and runnable by the operator hands-on
<!-- AC:END -->
