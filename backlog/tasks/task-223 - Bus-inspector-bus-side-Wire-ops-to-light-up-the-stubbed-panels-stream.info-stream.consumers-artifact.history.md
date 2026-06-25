---
id: TASK-223
title: >-
  Bus inspector: bus-side Wire ops to light up the stubbed panels (stream.info /
  stream.consumers / artifact.history)
status: To Do
assignee: []
created_date: '2026-06-25 02:31'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-bus
dependencies: []
priority: low
ordinal: 212000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Bus inspector (TASK-195/196) ships with three panels stubbed because the browser credential is an allow-list account denied $JS.API.> and $KV.> (PERMISSIONS_VIOLATION). Add three small bus-side Wire ops the dash can call over its own connection — stream.info (full stream config chips), stream.consumers (Consumers tab), artifact.history (KV revision history + tombstones) — then wire the stubbed panels to them. No permission change to the browser cred; the op runs over the bus's full-access path.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 stream.info Wire op returns a stream's full config; the inspector's Config tab renders it (no 'needs a bus-side op' notice)
- [ ] #2 stream.consumers Wire op returns a stream's consumers; the Consumers tab renders them
- [ ] #3 artifact.history (or kv.history) Wire op returns a key's revision history; the KV revision-history panel + tombstones render
- [ ] #4 No change to the browser credential's allow-list; ops run over the bus's own full-access connection
<!-- AC:END -->
