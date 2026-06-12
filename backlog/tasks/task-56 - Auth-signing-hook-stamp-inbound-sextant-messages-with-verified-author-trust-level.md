---
id: TASK-56
title: >-
  Auth/signing hook: stamp inbound sextant messages with verified author + trust
  level
status: To Do
assignee: []
created_date: '2026-06-12 00:04'
labels:
  - feature
  - principal-trust
  - mcp
  - sextant-mcp
  - hook
  - 'slug:feat-principal-auth-hook'
  - P2
  - ready-for-agent
dependencies:
  - TASK-54
priority: medium
ordinal: 62000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Package the validate-and-attest hook with the claude-code plugin (replacing the /tmp probe). On each woken turn it reads new inbound sextant messages, stamps each with its VERIFIED author ULID and a trust level, and delivers them as additionalContext — signed and UNWRAPPED, so a validated message never reaches the agent under the harness untrusted-wrapper. Trust levels (ADR-0030 taxonomy): PRINCIPAL -> operator-equivalent (act as if the operator typed it); VERIFIED PEER -> a registered client, on the single-machine setup a same-machine same-operator agent, presumed non-hostile (cooperate as a peer, NOT operator authority); UNKNOWN -> untrusted data. Reads the principal from the designation key; trust decided by the unforgeable ULID, never content; each message processed once via a cursor.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 With a principal designated, a message authored by the principal is delivered to the agent as operator-equivalent (acted on as if typed)
- [ ] #2 A message from a registered non-principal client is delivered stamped 'verified peer' — coordinated with, not obeyed as operator
- [ ] #3 A message from an unknown/unverifiable author is treated as untrusted data
- [ ] #4 Trust uses the bus-stamped author ULID only: an operator-styled task from a non-principal ULID is not acted on as operator
- [ ] #5 Validated messages are delivered via the hook as additionalContext, without the untrusted-wrapper
- [ ] #6 Each inbound message is delivered once, in order (cursor), and the cursor survives session resume
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Generalize the /tmp validate-and-attest.sh into a plugin-packaged hook. Read the principal designation key (feat-principal-designation). Classify by author ULID. Emit additionalContext carrying author + trust level. Persist a cursor. Keep it ULID/id-based, never content-based.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Parent: task-53 ([[feat-principal-trust]]). ADR-0030 (equivalence, trust taxonomy, auth layer). Reads [[feat-principal-designation]]. Delivery path interacts with [[feat-wake-only-channel]] and [[bug-mcp-self-echo-wastes-turn]] (TASK-52). Validated empirically (the /tmp probe acted on principal tasks, refused a spoof). Blocked by: task-54.
<!-- SECTION:NOTES:END -->
