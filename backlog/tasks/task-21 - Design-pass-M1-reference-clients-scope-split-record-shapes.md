---
id: TASK-21
title: 'Design pass: M1 reference clients (scope, split, record shapes)'
status: To Do
assignee: []
created_date: '2026-06-04 05:43'
labels: []
milestone: 'M1: Core protocol + SDK'
dependencies:
  - TASK-3
  - TASK-6
references:
  - docs/adr/0009-spawn.md
  - docs/adr/0011-workflows.md
  - docs/adr/0014-the-tui-is-a-client.md
priority: medium
ordinal: 6500
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Design the M1 reference clients before building them, and decide how TASK-7 splits. TASK-7 names three forkable clients (human-messaging UI / dash, spawn Dispatcher, sextant.workflow/v1 coordinator), each mapping to a large ADR end-state. This task settles, for M1: (1) depth per client — minimal end-to-end exercise vs richer surface; (2) the split — almost certainly one ticket per client, with TASK-7 becoming an umbrella or being replaced; (3) the concrete record shapes the minimal versions need (spawn-request fields incl. lineage job/parent per ADR-0009; workflow Layer-0 state envelope + sextant.workflow/v1 fields per ADR-0011); (4) where clients live and how they run (standalone go run vs a launcher). Output: a short design/spec doc + the refined tickets. A dash-TUI prototype is being built first to iterate the human surface against Lena's eye and feed this design.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Per-client M1 depth decided and written down (minimal-vs-rich, with rationale)
- [ ] #2 TASK-7 split decision made and the per-client tickets created/linked
- [ ] #3 Minimal record shapes specified: spawn-request (with lineage) and workflow Layer-0 state envelope
- [ ] #4 Run/layout decision: where reference clients live and how they are launched in M1
<!-- AC:END -->
