---
id: TASK-56
title: >-
  Auth/signing hook: stamp inbound sextant messages with verified author + trust
  level
status: In Progress
assignee: []
created_date: '2026-06-12 00:04'
updated_date: '2026-06-12 01:33'
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
- [x] #1 With a principal designated, a message authored by the principal is delivered to the agent as operator-equivalent (acted on as if typed)
- [x] #2 A message from a registered non-principal client is delivered stamped 'verified peer' — coordinated with, not obeyed as operator
- [x] #3 A message from an unknown/unverifiable author is treated as untrusted data
- [x] #4 Trust uses the bus-stamped author ULID only: an operator-styled task from a non-principal ULID is not acted on as operator
- [x] #5 Validated messages are delivered via the hook as additionalContext, without the untrusted-wrapper
- [x] #6 Each inbound message is delivered once, in order (cursor), and the cursor survives session resume
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Generalize the /tmp validate-and-attest.sh into a plugin-packaged hook. Read the principal designation key (feat-principal-designation). Classify by author ULID. Emit additionalContext carrying author + trust level. Persist a cursor. Keep it ULID/id-based, never content-based.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented as `sextant-mcp attest` subcommand (cmd/sextant-mcp/attest.go) — same binary as the MCP server, reusing its clictx per-session identity/context resolution (ADR-0029) so the hook connects as the same worker identity and scans its own DM (msg.client.<self>, TASK-55). Testable core in internal/attest (Classify/Stamp/BuildContext + Cursor). Plugin wiring: clients/claude-code/hooks/hooks.json invokes `sextant-mcp attest` (mirrors .mcp.json invoking sextant-mcp by name on PATH). Cursor: per-session JSON under $CLAUDE_PLUGIN_DATA/attest-cursor/<session-id>.json, keyed on CLAUDE_CODE_SESSION_ID, advanced by FetchMessages since_seq cursor. Trust by author ULID only (ADR-0030). Tests: internal/attest unit tests (classification/wording/spoof/cursor) + tests/e2e/attest_hook_test.go (principal/peer/unknown via retire, re-run delivers nothing). Degrades to exit 0 with no additionalContext on any bus error, bounded 5s under the 30s hook timeout. TASK-57: only the MCP channel push side changes to wake-only; this hook is already the sole content path. Live finding: a running pre-TASK-54 bus returns 'unknown operation principal.get' — hook degrades to no-principal; the operator must update their bus binary (TASK-53 operator-update AC).
<!-- SECTION:NOTES:END -->
