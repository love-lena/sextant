---
id: TASK-70
title: >-
  M5.1 spawn spike: PoC dispatcher that launches Claude + Codex agents onto the
  bus
status: Done
assignee: []
created_date: '2026-06-12 21:07'
updated_date: '2026-06-13 01:16'
labels:
  - feature
  - spawn
  - m5
  - orchestration
  - spike
  - 'slug:feat-m5-spawn-spike'
  - P1
  - ready-for-agent
dependencies: []
priority: high
ordinal: 75000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
De-risk the spawn milestone: prove that a dispatcher can launch an agent (claude -p, codex exec) that (a) joins the bus under its own identity via auto-mint, (b) runs a task (publish a hello on a topic), (c) returns its new client id + result, and (d) wakes and acts on a follow-up bus message (shape-1 supervisor: subscribe → re-invoke --resume on inbound DM). This is a PoC, not production code — the artefact is a working demo + per-harness design notes (launch recipe, identity seam, wake-loop, prompt injection, result plumbing) that feed M5.2 (the real dispatcher + mint-on-behalf). Research pre-done in m5-spawn-spike-research; this ticket turns it into running code. The client wrapper each harness hands to the SDK is its own self-contained client (implements the SDK) so it can grow without coupling to the dispatcher later.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 claude -p harness: a spawned Claude agent joins the bus under a keyed (resume-stable) identity, publishes a hello on msg.topic.demo, and its client id is returned to the dispatcher caller
- [x] #2 codex exec harness: a spawned Codex agent self-enrolls (store + enroll.creds in MCP env), joins the bus, publishes a hello, and its client id is returned
- [x] #3 Wake loop (shape 1): the PoC wraps one of the one-shots in a thin supervisor that subscribes to the agent's DM and re-invokes it (--resume / codex exec resume) on inbound — the agent wakes, reads the message, and acts on it
- [x] #4 Nicknames: each spawned agent (kind=agent) gets a human-readable nickname, not the claude-<hex> auto-mint placeholder
- [x] #5 Design notes written up (per harness): launch recipe, identity seam gaps vs mint-on-behalf, wake-loop shape, primer injection
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
PoC only — no core protocol changes. Harness A: shell out to claude -p with --mcp-config pointing at sextant-mcp; capture session_id + result from JSON envelope. Harness B: codex exec with sextant-mcp in MCP config env (store + enroll.creds). Wake loop: a thin Go or shell supervisor that subscribes to msg.client.<spawned-id> via sextant subscribe (Monitor), re-invokes the harness with --resume on inbound. Nickname: pass a display name at spawn time (flag or env). Output: a demo script + design-notes doc. Feeds M5.2 dispatcher design.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Research: m5-spawn-spike-research artifact (rev 16). Lena approved on msg.topic.orchestration-m5 2026-06-12. Successor: [[feat-m5-client-standup]] (M5.2, mint-on-behalf). Parallel: [[feat-m5-sextant-run]] (M5.3).

PoC built on branch worktree-task-70-spawn-spike. Supervisor: cmd/spawn-poc (its own pkg/sextant client — Connect + Subscribe(msg.client.<agent>, DeliverAll) + re-invoke --on-wake, threading inbound text via $SX_WAKE_TEXT; --once/--deadline/--wake-timeout fail-loud). Self-validating docs/demos/spawn-spike-demo.sh: 6 passed / 0 failed on a throwaway bus — AC#1 (claude -p keyed id), AC#2 (codex exec), AC#3a (supervisor mechanism, token-free) + AC#3b (live claude -p --resume: woken agent rejoined under its SAME keyed id and published awake-ack), AC#4 (nickname vega). AC#5 notes: docs/demos/spawn-spike-notes.md. Findings: MCP server is intermittently 'pending' on --resume (first tool call -> 'No such tool available'); mitigated by a retry primer in the wake adapter. Exit-hook refinement (lena 2026-06-12) folded in: agent's Stop hook declares topics -> supervisor --watch set. No core changes (mint-on-behalf deferred to M5.2/TASK-25).
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
M5.1 spawn spike shipped to main in PR #121 (cmd/spawn-poc + docs/demos/spawn-spike-demo.sh, 6/6 ACs): a dispatcher can launch claude -p / codex exec agents that join the bus under their own identity and wake on a DM, no core protocol change. Closed + archived as a fold-in of the M5.2 PR (TASK-25) per the no-standalone-status-flip agreement.
<!-- SECTION:FINAL_SUMMARY:END -->
