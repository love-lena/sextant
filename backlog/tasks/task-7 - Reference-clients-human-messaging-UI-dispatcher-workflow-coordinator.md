---
id: TASK-7
title: 'Reference clients: human-messaging UI, dispatcher, workflow coordinator'
status: To Do
assignee: []
created_date: '2026-06-03 01:12'
updated_date: '2026-06-04 17:56'
labels: []
milestone: 'M2: MVP'
dependencies:
  - TASK-3
  - TASK-6
  - TASK-21
references:
  - docs/adr/0009-spawn.md
  - docs/adr/0011-workflows.md
priority: medium
ordinal: 7000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Forkable reference clients that validate the design end-to-end: a bus-native human-messaging client (the dash, ADR-0014), a spawn-request Dispatcher (ADR-0009), and a sextant.workflow/v1 coordinator (ADR-0011). Blocked on the design pass (TASK-21), which settles per-client M1 depth and will most likely split this into one ticket per client; TASK-7 then becomes the umbrella (or is replaced by the splits). A dash-TUI prototype is being built first to iterate the human surface against Lena's eye and feed TASK-21.
<!-- SECTION:DESCRIPTION:END -->
