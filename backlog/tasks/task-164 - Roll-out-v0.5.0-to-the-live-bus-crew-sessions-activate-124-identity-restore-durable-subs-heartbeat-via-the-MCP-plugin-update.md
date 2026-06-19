---
id: TASK-164
title: >-
  Roll out v0.5.0 to the live bus + crew sessions (activate 124 identity-restore
  + durable subs + heartbeat via the MCP plugin update)
status: To Do
assignee: []
created_date: '2026-06-18 00:24'
updated_date: '2026-06-18 00:28'
labels: []
dependencies: []
priority: medium
ordinal: 154000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
v0.5.0 is released and the binaries are current on Lena's machine (sextant v0.5.0 / 83aa57d via brew; plugin v0.5). VERIFIED (canopus, 2026-06-18): 124 is LIVE — the MCP wrote a v0.5-schema substate file (mcp-substate/<session>.json = {context, subjects:{<subj>:{seq}}}, the new {seq} struct), proving the running MCP is v0.5. The one-time auto-mint everyone saw on the FIRST post-upgrade resume was the EXPECTED transition: the pre-124 MCP never wrote substate, so the first resume found nothing to restore and auto-minted once; a context_use then SEEDS the substate so every subsequent resume auto-restores identity + subs with no manual step. NO cold restart needed (an earlier 'stale MCP process' theory was traced + retracted). Rollout is effectively complete; this records it + the verification. Related: TASK-124/ADR-0037, TASK-76 (original auto-mint report), TASK-126/ADR-0036 (heartbeat), durable subs.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 brew upgrade the sextant tap formula → 'sextant version' reports v0.5.0 (bus/CLI binary)
- [ ] #2 restart the bus service so it runs the v0.5.0 binary
- [ ] #3 verify identity-restore: on a session resume, it auto-restores the prior context_use'd identity with NO manual context_use (TASK-124/ADR-0037)
- [ ] #4 verify subs-restore: on resume, subscriptions auto-restore with NO manual re-subscribe (durable subs)
- [ ] #5 verify heartbeat presence: a leaf/remote agent reads online via heartbeat last_seen (ADR-0036)
- [ ] #6 Binaries already current (verified): brew sextant-mcp on PATH = v0.5.0 (83aa57d) + plugin = v0.5. 'claude plugin update' covers skills/hooks but does NOT change the sextant-mcp binary (that ships via brew)
- [ ] #7 VERIFIED 124 is live (no cold restart needed): the MCP wrote a v0.5-schema substate ({context, subjects:{seq}}). The one-time auto-mint was the pre-124-to-v0.5 transition (no substate on the first resume); after a context_use seeds it, subsequent resumes auto-restore. Confirm on Lena's session: after her next resume, identity + subs restore with NO manual context_use.
<!-- AC:END -->
