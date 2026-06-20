---
id: TASK-184
title: 'One-command live demo: a pi agent on the operator''s bus, end to end'
status: Done
assignee: []
created_date: '2026-06-19 21:31'
updated_date: '2026-06-20 04:47'
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

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
One-command live pi-on-the-bus CAPSTONE demo SHIPPED (PR #243 squash). A /slash-command skill (clients/claude-code/skills/pi-live-demo) + self-validating docs/demos/pi-live-demo.sh (reuses 177 driven + 180 dash + 178 recipe). ORCHESTRATOR RAN the documented one-command (no env overrides, real model, build-from-clean, hermetic): 4/4 PASS — AC#1 throwaway bus + DISTINCT pi identity; AC#2 operator DMs → wake + reply + /set-goal moves a criterion (goal.update, dash renders); AC#3 10 activity frames (turn/tool/thinking/message) → dash auto-renders; AC#4 sextant dash --serve live (co-equal TS client over wss). Hermetic (real home active=lena untouched, verified). Folded in the deferred TASK-172 demo-path cleanup (10+ scripts: stale cmd/ → clients/go/apps). Self-sufficient from clean.
<!-- SECTION:FINAL_SUMMARY:END -->
