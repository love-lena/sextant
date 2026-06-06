---
id: TASK-7
title: 'Reference client: the human-messaging dash'
status: To Do
assignee: []
created_date: '2026-06-03 01:12'
updated_date: '2026-06-04 18:11'
labels: []
milestone: 'M4: The dash (human UI)'
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
The dash (ADR-0014): one Bubble Tea client over the Go SDK composing pane-surfaces — presence (ListClients), the dialogue stream (Subscribe), an artifact review card, and a read-only workflow view. Per the proto/dash-tui verdict it is a composable, customizable pane library (sensible defaults, but swapping/arranging panes is first-class, btop-style), with the prototyped workflow (Checklist/Timeline/Pipeline) and artifact (Reader/Review + inline comments) variants as built-in options; cockpit default layout; detail-on-demand. Split out of the old reference-clients bundle: the Dispatcher (TASK-25) and Workflow coordinator (TASK-26) are deferred to M4; the MVP is manual-comms only.
<!-- SECTION:DESCRIPTION:END -->
