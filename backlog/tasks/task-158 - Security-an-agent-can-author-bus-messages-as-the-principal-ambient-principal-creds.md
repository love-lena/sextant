---
id: TASK-158
title: >-
  Security: an agent can author bus messages as the principal (ambient principal
  creds)
status: To Do
assignee: []
created_date: '2026-06-17 20:55'
updated_date: '2026-06-18 01:06'
labels:
  - bug
  - security
  - auth
  - principal
  - P1
  - ready-for-human
  - 'slug:bug-agent-can-impersonate-principal'
dependencies: []
priority: high
ordinal: 148000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena identified this 2026-06-17 from sirius's violet testing: sirius (an agent) ran `sextant publish` and the messages were bus-stamped author=lena (the PRINCIPAL), because the bare CLI resolves the operator's active context whose creds (the principal's) are ambiently available to agent processes in the operator's environment. So any agent/process there can IMPERSONATE the principal on the bus (author messages as the operator). The bus correctly binds authorship to the creds used; the gap is that the PRINCIPAL'S creds are ambiently usable by non-principal processes (no isolation). This undermines the principal-trust model (a forged principal message is operator-equivalent to every client).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A non-principal process/agent CANNOT produce a bus message stamped author=<principal> (the RED case fails to impersonate)
- [ ] #2 Agents run with their own scoped creds; the principal's creds are not ambiently present in the agent execution environment
- [ ] #3 A regression test encodes the RED case: a publish without the principal's identity is rejected / not stamped as the principal
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
RED test established live (sirius published as lena via the active context). Fix direction: scope/isolate creds so agent processes hold only their own identity; the operator's creds must not be ambient where agents run. Likely an ADR + an auth/cred-isolation change. Core/security owner (vega).
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered: Lena 2026-06-17 via sirius testing violet as the active (lena) context. Relates: ADR-0029 (per-session identity), ADR-0030 (principal trust), [[reference_bare_sextant_cli_resolves_real_context]]. Owner: vega/core. Not v0.5.0-introduced (pre-existing), but P1.

Facet (codex flag via TASK-76, 2026-06-17): the explicit-context path in cmd/sextant-mcp/conn.go resolve() (the $SEXTANT_CONTEXT/--context branch, ~:121-130) has NO agent-kind guard, unlike context_use (which refuses non-agent). So $SEXTANT_CONTEXT=<human/principal context> connects as that identity — a launch-time path to principal-impersonation that bypasses the 'never speak as a person' refusal. Pre-existing (NOT introduced by TASK-76; TASK-76 stays the doc fix). Fix candidates: (a) enforce agent-kind on the explicit path (defense-in-depth), or (b) confirm 158's cred-isolation subsumes it (an agent shouldn't hold the principal's saved creds to point at). canopus's area (resolve/conn.go).
<!-- SECTION:NOTES:END -->
