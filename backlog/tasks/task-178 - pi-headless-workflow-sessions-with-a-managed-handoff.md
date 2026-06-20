---
id: TASK-178
title: pi headless workflow sessions with a managed handoff
status: Done
assignee: []
created_date: '2026-06-19 21:11'
updated_date: '2026-06-20 03:23'
labels:
  - feature
  - pi
  - workflow
  - dispatcher
  - 'slug:feat-pi-headless-session-handoff'
  - P3
  - ready-for-agent
dependencies:
  - TASK-177
ordinal: 168000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Run pi sessions as headless workflow workers, addressable over the bus (primary), with a managed close-and-resume handoff (secondary). The dispatcher spawns a pi session as a scoped bus client; the operator interacts via bus DM/topic. For a hands-on handoff: a bus signal triggers cooperative Stop/Drain (session persists), the operator resumes it by hand, the dispatcher re-spawns to resume - single-owner-at-a-time, so nothing fights. PRD doc-2.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 the dispatcher can spawn and re-spawn a pi session as a scoped bus client
- [ ] #2 a headless pi worker is addressable over the bus and responds like a crew member
- [ ] #3 a bus-signalled drain, manual pi resume, and dispatcher re-spawn handoff works without two processes fighting the session
- [ ] #4 the operator (via dash DM) sends a task to the headless pi worker; it does the work and posts an artifact/reply the operator sees - indistinguishable from a Claude Code crew member in the dash
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
pi headless + managed handoff SHIPPED (PR #241 squash; fix a485664). Dispatcher pi-recipe (recipes/pi.sh, mint-on-behalf ADR-0033 — worker on its OWN scoped creds) + managed handoff (pi.handoff lexicon + cooperative drain: drain→relinquish→exit→re-spawn-resume, single-owner). ORCHESTRATOR INDEPENDENTLY RAN the AC#4 driven handoff (real model, hermetic bus, NO SEXTANT_REPO_ROOT): 4 findings 0 FAIL — recipe spawns a scoped addressable worker (own id, not operator); operator DMs a task → worker creates artifact + replies (visible like a crew member); drain→relinquish→EXIT (single-owner) then re-spawn→RESUME (recalled the pre-handoff secret = JSONL resumed not restarted). Fix round: driven:handoff repoRoot off-by-one (4 .. from dist/test → doubled clients/clients recipe path → SKIP) → fixed via go.mod walk, re-verified by me. Hermetic (real home active=lena untouched). Gate green: Go 32 ok + make lint, TS pi unit 30/30; no new Go.
<!-- SECTION:FINAL_SUMMARY:END -->
