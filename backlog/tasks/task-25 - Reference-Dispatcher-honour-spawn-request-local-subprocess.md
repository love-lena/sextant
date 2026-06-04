---
id: TASK-25
title: 'Reference Dispatcher: honour spawn-request (local subprocess)'
status: To Do
assignee: []
created_date: '2026-06-04 18:05'
updated_date: '2026-06-04 18:11'
labels: []
milestone: 'M5: Orchestration (spawn + workflows)'
dependencies: []
references:
  - docs/adr/0009-spawn.md
priority: medium
ordinal: 24000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Deferred out of the MVP (which uses manually-started clients). The reference Dispatcher: subscribe to spawn-request messages and launch a client however it chooses (local subprocess to start). The spawned client connects as an ordinary participant. Lineage (job/parent) is a correlation field in the record. Spawn-on-demand needs a Dispatcher running (ADR-0009).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 spawn-request message kind defined (with job/parent lineage)
- [ ] #2 Dispatcher subscribes to spawn-request and launches a client (local subprocess)
- [ ] #3 The spawned client connects under its own identity and participates
- [ ] #4 Recursion works: a spawned client can itself publish spawn-requests
<!-- AC:END -->
