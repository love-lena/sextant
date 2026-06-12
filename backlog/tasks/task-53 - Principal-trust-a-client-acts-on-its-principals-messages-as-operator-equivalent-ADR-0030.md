---
id: TASK-53
title: >-
  Principal trust: a client acts on its principal's messages as
  operator-equivalent (ADR-0030)
status: To Do
assignee: []
created_date: '2026-06-12 00:03'
updated_date: '2026-06-12 02:41'
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
- [ ] #5 The demo drives a REAL Claude Code worker session through the genuine hook/channel path, using production-normal names for the bus, working dir, and clients (no test/demo/probe tells) so the worker is never tipped off it is a demo
- [ ] #6 Demo scene order: (1) principal task -> acted on as operator-equivalent; (2) peer coordination with a genuine collaborative ask (e.g. a verified peer requests a PR review of the worker's output) -> worker cooperates as a peer, not as operator; (3) AFTER the collaboration, a spoofed operator-styled task from a non-principal -> refused, real author named by ULID; (4) designation enforcement -> client-tier re-point denied, Operator re-point succeeds; the epilogue self-validates PASS/FAIL
- [ ] #7 Documents the operator update path: states whether landing these changes needs a new GitHub release and/or a plugin update, with the exact steps Lena runs to update her sextant install (or an explicit statement that no update is needed)
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Integrate the child slices and ship a self-contained demo per the live-demo skill. A human runs the demo and signs off (ready-for-human). No implementation in this ticket.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
STATUS 2026-06-11: all six child slices (TASK-54/55/52/56/57/58) implemented + verified on this branch (PR #109); gofumpt/vet + go test -race + e2e all green. Adversarial spec-review: NO Critical — trust model proven sound (author ULID unforgeable; classification reads only the bus-stamped author, never content; operator-only write-gate holds with real client creds). Two Majors found + fixed: M1 (a principal DM now wakes the session — the MCP server bridges the auto-DM channel into the channel-wake path; also fixed a latent bug where the connManager tore down the auto-DM sub on the per-request context) and M2 (attest cursor is at-most-once on successful emit). Minors documented in code.

DEMO: clients/claude-code/demo-principal-trust.sh (one-command, live-demo skill anatomy, production-normal names: operator/principal lena, worker mira, peer devon, non-principal kai). Backbone verified mechanically: staging, principal designation + client read, DM round-trip, scene-5 designation enforcement (client-tier set DENIED / operator re-point OK), isolation to the throwaway bus. The live worker scenes (2 principal task, 3 peer coordination, 4 spoof) are the hands-on SIGN-OFF run — they exercise the real plugin hook + the channel-wake (the research-preview wake behavior the demo validates; Monitor is the documented fallback).

AC#7 (operator update path): landing this needs Lena to update her running install — build + reinstall the bus/CLI/plugin from this branch (her current bus predates TASK-54, so principal.* ops are unknown to it until updated). Exact steps to confirm in the demo/PR.

FORWARD RISK (for the eventual rebase onto main): this branch predates ADR-0029/PR #107 (per-session identity keyed on CLAUDE_CODE_SESSION_ID). `sextant-mcp attest` and the MCP connManager both resolve identity via clictx.Resolve symmetrically today; on rebase, the session-keyed selection must route THROUGH clictx.Resolve (or a shared resolver) or attest scans the wrong client's DM. Marked in code at cmd/sextant-mcp/attest.go.
<!-- SECTION:NOTES:END -->
