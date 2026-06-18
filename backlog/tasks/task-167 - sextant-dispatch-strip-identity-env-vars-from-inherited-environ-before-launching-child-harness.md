---
id: TASK-167
title: >-
  sextant-dispatch: strip identity env vars from inherited environ before
  launching child harness
status: To Do
assignee: []
created_date: '2026-06-18 04:44'
labels:
  - feature
  - dispatch
  - security
  - hardening
  - 'slug:feat-dispatch-strip-identity-env'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 157000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Defense-in-depth for TASK-158 creds isolation on the dispatcher spawn path. launchCmd does `cmd.Env = append(os.Environ(), childEnv...)`; if the dispatcher was started via $SEXTANT_CREDS, the child's env carries a DUPLICATE SEXTANT_CREDS (dispatcher's first, child's last). Isolation HOLDS today (POSIX/Go last-wins at every layer + the recipe's explicit MCP env block pins the child's creds for sextant-mcp, the real consumer; adversarially verified on PR #197), but it rests on last-wins semantics across three process layers. Stripping SEXTANT_CREDS/SEXTANT_STORE/SEXTANT_CONTEXT from os.Environ() before appending childEnv makes the isolation invariant LOCAL (exactly one entry) instead of behavioral. Not a current bug; pure hardening.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 launchCmd (or the harness env assembly) removes SEXTANT_CREDS, SEXTANT_STORE, SEXTANT_CONTEXT from os.Environ() before appending childEnv; SEXTANT_MCP_BIN, ANTHROPIC_API_KEY, PATH and other inherited env are preserved
- [ ] #2 the child harness env contains exactly one SEXTANT_CREDS entry = the child's minted creds (a test asserts no duplicate identity var reaches the child)
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
In cmd/sextant-dispatch/main.go launchCmd, filter os.Environ() to drop the three SEXTANT_* identity keys, then append childEnv; add a unit test on the env-assembly helper.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: PR #197 adversarial creds-isolation gate (the duplicate-SEXTANT_CREDS trace). Related: [[feat-principal-hardening]], the M5 dispatcher mint-on-behalf (ADR-0033). Isolation already holds; this removes reliance on last-wins.
<!-- SECTION:NOTES:END -->
