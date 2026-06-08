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
  - docs/adr/0023-the-dash-is-a-composable-pane-cockpit.md
  - docs/adr/0014-the-tui-is-a-client.md
priority: medium
ordinal: 7000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Umbrella for the M4 dash build — the forkable reference human-UI client
(ADR-0014): a composable pane cockpit over the SDK. The design was settled in the
TASK-21 pass; the customization mechanism (presets + toggle + reflow + config) and
the widget → surface → dash contract are **ADR-0023**. Fans out into subtasks:
TASK-7.1 theme + widget toolkit · 7.2 SDK→tea adapter · 7.3 surfaces (presence ·
message-stream · artifact) · 7.4 layout engine · 7.5 the dash binary. M4 panes =
presence + message-stream + artifact; the workflow pane and the reference
Dispatcher (TASK-25) + Workflow coordinator (TASK-26) are M5. MVP is manual-comms.
<!-- SECTION:DESCRIPTION:END -->
