---
id: TASK-50
title: sextant-mcp inherits the operator's active context — agent speaks as the human
status: Done
assignee: []
created_date: '2026-06-11 04:14'
updated_date: '2026-06-11 05:15'
labels:
  - ready-for-agent
dependencies: []
priority: high
ordinal: 56000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The plugin's MCP server (cmd/sextant-mcp) reuses the operator CLI's identity chain verbatim: clictx.Resolve -> explicit $SEXTANT_CREDS / $SEXTANT_CONTEXT, then clictx.Active() (conn.go:80, clictx.go:219-221). The committed clients/claude-code/.mcp.json pins nothing ({"command":"sextant-mcp"}) and context names are per-machine so it CAN'T pin one, so the server falls all the way to the human's active context. Result: every tool call the agent makes is bus-stamped with the operator's unforgeable ULID — the agent literally speaks AS the human. Caught live: clients_list showed one online client 'lena', and the active context resolved to that same ULID; the MCP server WAS lena. This subverts ADR-0020 (clients are bus-issued identities / unforgeable authorship). The Active() fallback is correct for the CLI (kubectl pattern) and wrong for the MCP server. Decision taken with Lena (2026-06-10): the MCP server must NEVER use the Active() fallback; when nothing is pinned it auto-provisions a DEDICATED agent identity (non-activating self-enroll) and connects as it. Note: register --self currently activates what it mints (ops.go:274-289), so even the documented 'give the agent its own context' recipe would clobber the operator's active context — the non-activating enroll path is new.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 sextant-mcp identity resolution does NOT fall back to clictx.Active(); explicit $SEXTANT_CREDS/$SEXTANT_CONTEXT (and --creds/--context) still resolve exactly as before
- [x] #2 With nothing pinned, the MCP server self-enrolls a dedicated agent context and connects as it WITHOUT changing the active context (clictx.Active() returns the same value before and after server start)
- [x] #3 Regression test: with active context X set and no env pins, the MCP server connects as an identity != X, and clictx.Active() still == X afterward
- [x] #4 The CLI's clictx.Resolve / Active() fallback is unchanged — the divergence is MCP-only
- [x] #5 Behaviour recorded in canon: ADR (new, or amendment to ADR-0028/ADR-0021) + the sextant skill's 'Identity setup' section corrected (it currently documents the Active() fallback and a register --self that activates)
- [x] #6 An MCP tool lets Claude explicitly switch/resume to an existing context by name or id (mirrors 'sextant context use'); minting-new is the default ONLY when Claude has not explicitly switched
- [x] #7 When minting is required but <store>/enroll.creds is absent or the bus is unreachable, the tool call returns an actionable error AND the server stays up and self-heals once an identity becomes available — it NEVER borrows Active()
- [x] #8 Each distinct Claude session (keyed by CLAUDE_CODE_SESSION_ID) lazily mints its OWN dedicated context on first bus call; two concurrent sessions get distinct identities, so they never both answer the same message
- [x] #9 Resuming a session re-attaches to that session's existing context instead of minting a new one: resolving twice with the same CLAUDE_CODE_SESSION_ID yields the same context/ULID; two different session ids yield two different contexts
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
MCP-only resolve, NO Active() fallback ever. Order: $SEXTANT_CREDS -> $SEXTANT_CONTEXT -> explicitly-switched context (this process) -> context stamped with this CLAUDE_CODE_SESSION_ID (reattach) -> else lazily mint a NEW dedicated context (non-activating selfenroll: skip SetActive; unique handle e.g. claude-<short-ulid>, display 'claude'), stamping CLAUDE_CODE_SESSION_ID onto the context record so the store is the single source of truth and resume reattaches automatically. Add an MCP tool (context_use) for deliberate switching to a DIFFERENT identity. Leave clictx.Resolve/Active() untouched for the CLI. enroll.creds absent / bus unreachable -> per-call actionable error; conn.go already defers+retries so it self-heals; never borrow Active(). If CLAUDE_CODE_SESSION_ID is unset (non-CC host), fall back to mint-new each process (no resume key) — still never Active().
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: 2026-06-10 attempt to DM a client over the running bus; the only online client WAS the agent's own (inherited) identity. Open design sub-decisions for the human: (1) agent context name/display + scoping — per-machine constant ('claude-code') vs per-project; (2) ADR new vs amend 0028; (3) enrollment-credential-absent behaviour. Related: TASK-46 (shared-identity detection — surfaces the symptom when two sessions share one ULID), TASK-31 (ADR-0021 contexts + register --self auto-context), ADR-0020 (bus-issued identities), ADR-0028 (plugin adapter). [[bug-mcp-inherits-active-context]]

DECISIONS (2026-06-10, with Lena):
- NEW ADR (not an amendment) records this — the MCP/harness identity model.
- Identity-per-harness, NOT a shared 'claude-code' identity: each harness launch mints its own context. Rationale: a shared identity means multiple live harnesses are subscribed as one client and would all answer the same message. Per-harness ULIDs keep authorship distinct.
- Explicit switch/resume is opt-in: Claude can deliberately re-attach to an existing context (new MCP tool), but the default is mint-new.
- #3 (enroll.creds absent / bus unreachable): RECOMMENDED = per-call actionable error + server stays up and self-heals (conn.go already defers/retries). There is no safe 'softer' path — the only non-erroring alternative is borrowing an existing identity (Active()), which is the very impersonation bug being fixed; read-only degrade is impossible because reads also need an identity. PENDING Lena's confirm.
- enroll.creds lives at <store>/enroll.creds (selfenroll.go:42); it is the low-privilege bootstrap key, distinct from the operator credential.

FINAL DESIGN (2026-06-10, confirmed by Lena): session-keyed auto-attach.
- Resume viability CONFIRMED: running sextant-mcp (CC 2.1.173) has CLAUDE_CODE_SESSION_ID set + CLAUDE_PLUGIN_DATA dir. Session-id present/readable verified empirically; resume-stability per CC changelog v2.1.163 ('MCP servers get same session id on resume'), we're past it. Could not prove the resume half from inside one session.
- Identity is keyed by CLAUDE_CODE_SESSION_ID and stamped on the context record (NOT a separate map file) -> resume reattaches automatically, concurrent sessions stay distinct.
- Explicit setup REJECTED in favour of auto-attach: auto is now correct across resume, so requiring a manual register/use ritual would only add friction AND reintroduce the dup-identity footgun (forgetting to reattach on resume). What stays explicit is switching to a DIFFERENT identity (context_use tool).
- #3 CONFIRMED as recommended (per-call error + self-heal, never Active()).

Implemented on PR #107 (commit a4e191a, branch worktree-task-50-mcp-identity). All 9 ACs evidenced by unit + e2e tests; full CI gate (vet, gofumpt, go test -race, e2e) green locally. context_use carries a Kind==human refusal guard (flagged in the PR for Lena's call). Left In Progress pending PR review = sign-off.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Fixed in 3b44b5d (PR #107, ADR-0029): sextant-mcp provisions its own per-session identity (keyed by CLAUDE_CODE_SESSION_ID, resume-stable) and never inherits the operator's active context. Explicit $SEXTANT_CREDS/$SEXTANT_CONTEXT and an agent-only context_use switch take precedence; selfenroll grew a non-activating EnrollAgent. Self-review tightened context_use to an agent-kind allow-list. Unit + e2e (real bus) green.
<!-- SECTION:FINAL_SUMMARY:END -->
