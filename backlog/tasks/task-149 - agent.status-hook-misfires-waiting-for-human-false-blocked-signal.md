---
id: TASK-149
title: agent.status hook misfires waiting-for-human (false blocked-signal)
status: To Do
assignee: []
created_date: '2026-06-17 04:34'
labels:
  - bug
  - status
  - statushook
  - llm
  - 'slug:bug-status-hook-false-waiting-for-human'
  - P3
  - needs-triage
dependencies: []
priority: low
ordinal: 139000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The per-agent status hook (#132, internal/statushook + cmd/sextant-mcp/status.go — a PostToolUse→Haiku classifier) auto-set canopus's agent.status to state=waiting-for-human (headline 'awaiting review on Q&R documentation in ADR consequences') while canopus was NOT blocked — it was finalizing TASK-124 + self-resolving codex findings. The false 'blocked' signal led the coordinator (sirius) to send an unblock-ping (a false-escalation path; wasted a round-trip + risked a premature operator escalation). Root cause: the Haiku classifier over-eagerly maps 'awaiting review / finalizing / next step is a gate' → waiting-for-human, when the agent is actually still WORKING and self-resolving (no pending human input). Fix: tighten the classifier so waiting-for-human requires a GENUINE pending human input/decision the agent cannot proceed without — 'finalizing', 'awaiting my own codex round', 'will PR then gate' are WORKING, not waiting-for-human.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 agent.status does NOT set waiting-for-human when the agent is finalizing / self-resolving / its own next-step-is-a-future-gate (those are 'working')
- [ ] #2 waiting-for-human is set only when there is a concrete pending human input/decision blocking the agent now
- [ ] #3 classifier tested against samples: 'finalizing codex findings, will PR then gate' → working; 'blocked: need operator to approve X before I can proceed' → waiting-for-human
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered 2026-06-16 (v0.5.0 drive): canopus's status misfired waiting-for-human during 124 finalization → false blocked-signal → coordinator unblock-ping (canopus confirmed not-blocked). v0.4-era hook (#132). Minor friction, not v0.5.0-blocking.
<!-- SECTION:NOTES:END -->
