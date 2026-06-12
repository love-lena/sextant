---
id: TASK-53
title: >-
  Principal trust: a client acts on its principal's messages as
  operator-equivalent (ADR-0030)
status: To Do
assignee: []
created_date: '2026-06-12 00:03'
updated_date: '2026-06-12 00:04'
labels:
  - feature
  - principal-trust
  - epic
  - 'slug:feat-principal-trust'
  - P2
  - ready-for-human
dependencies:
  - TASK-54
  - TASK-55
  - TASK-56
  - TASK-57
  - TASK-58
priority: medium
ordinal: 59000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Umbrella for the principal-trust workstream (ADR-0030). A client treats its PRINCIPAL's messages — one human's client per bus, designated at bootstrap by the operator and bus-enforced — as fully equivalent to its operator's direct input, under the same harness measures as a direct prompt, with trust decided by the unforgeable author ULID (never content). This ticket is the HUMAN-REVIEW checkpoint: it tracks end-to-end integration plus a hands-on demo for sign-off. Implementation lives in the child slices; do not build here. Design: docs/adr/0030-clients-act-on-a-principals-messages-as-operator-input.md and .work/plans/inter-agent-trust-plan.md. Evidence: docs/agents/claude-code-trust-behavior.md.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Operator can designate and re-designate the bus principal; only the Operator credential can set it; clients discover it on connect and on change
- [ ] #2 A message authored by the principal is acted on by a client as operator-equivalent input, verified end-to-end
- [ ] #3 A message from a non-principal (verified peer or unknown) is NOT acted on as operator authority, while peer coordination still works
- [ ] #4 Trust is decided by the unforgeable author ULID, never message content: a spoofed operator-styled task from a non-principal ULID is refused
- [ ] #5 A one-command demo (demo.sh, live-demo pattern) stands up a throwaway bus, designates a principal, and lets a reviewer drive a client that acts on a principal message and refuses a spoofed non-principal task, self-validating in its epilogue
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Integrate the child slices and ship a self-contained demo per the live-demo skill. A human runs the demo and signs off (ready-for-human). No implementation in this ticket.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
ADR-0030. Plan: .work/plans/inter-agent-trust-plan.md. Evidence: docs/agents/claude-code-trust-behavior.md. Children: [[feat-principal-designation]], [[feat-client-auto-subscribe-own-dm]], [[feat-principal-auth-hook]], [[feat-wake-only-channel]], [[feat-sextant-skill-principal-trust]]. Related: [[bug-mcp-self-echo-wastes-turn]] (TASK-52). Demo built per the live-demo skill.
<!-- SECTION:NOTES:END -->
