---
id: TASK-7.3
title: 'Dash: surfaces (presence, message-stream, artifact)'
status: To Do
assignee: []
created_date: '2026-06-06 03:00'
labels: []
milestone: 'M4: The dash (human UI)'
dependencies:
  - TASK-7.1
  - TASK-7.2
references:
  - docs/adr/0023-the-dash-is-a-composable-pane-cockpit.md
  - docs/adr/0014-the-tui-is-a-client.md
  - docs/adr/0016-artifacts-are-lexicon-records.md
parent_task_id: TASK-7
priority: medium
ordinal: 32000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The surface stratum (ADR-0023): a Surface contract (set size, focus, render content, emit OpenMsg/DoneMsg, declare id+title for toggling) and the three M4 panes — presence (client records), message-stream (one read surface + optional compose; round-trip merge), artifact (document reader + review). Built on the toolkit + adapter, touching only the public SDK.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 a Surface contract the panes implement (size/focus, render, intents, id+title)
- [ ] #2 presence + message-stream(+compose) + artifact(reader/review) surfaces, public SDK only
- [ ] #3 each surface runs standalone and mounts as a pane unchanged; teatest goldens + PTY verify
<!-- AC:END -->
