---
id: TASK-178
title: pi headless workflow sessions with a managed handoff
status: To Do
assignee: []
created_date: '2026-06-19 21:11'
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
<!-- AC:END -->
