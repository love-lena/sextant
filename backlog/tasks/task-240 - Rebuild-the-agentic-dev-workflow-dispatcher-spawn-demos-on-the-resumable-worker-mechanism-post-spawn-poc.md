---
id: TASK-240
title: >-
  Rebuild the agentic-dev-workflow + dispatcher/spawn demos on the
  resumable-worker mechanism (post-spawn-poc)
status: To Do
assignee: []
created_date: '2026-06-26 19:23'
labels:
  - needs-triage
dependencies: []
ordinal: 228000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TASK-239 retired three M5-era demos (docs/demos/{agentic-dev-workflow,m5-dispatcher-demo,spawn-spike-demo}.sh) because they ran the M5.1 spawn-spike supervisor as a runtime wake-loop (--supervisor/--on-wake), which ADR-0045 reframed (a mobilized agent is a resumable one-shot function, pi --rpc). The supervisor binary was removed by the ADR-0049 restructure, so a faithful run needs the new mechanism. Rebuild these demos (especially the flagship agentic-dev-workflow gate->resume round-trip) on the current resumable-worker + coordinator/dispatcher path, or fold their coverage into m5-workflow-demo.sh (which was re-pointed and passes 8/8). The stubs in place point readers to clients/dispatcher (+ recipes), m5-workflow-demo.sh, and the design notes.
<!-- SECTION:DESCRIPTION:END -->
