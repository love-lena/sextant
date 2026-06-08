---
id: TASK-7.5
title: 'Dash: the binary (cmd/sextant-dash + sextant dash alias)'
status: To Do
assignee: []
created_date: '2026-06-06 03:00'
labels: []
milestone: 'M4: The dash (human UI)'
dependencies:
  - TASK-7.4
references:
  - docs/adr/0023-the-dash-is-a-composable-pane-cockpit.md
  - docs/adr/0014-the-tui-is-a-client.md
  - docs/adr/0008-clients-are-processes.md
parent_task_id: TASK-7
priority: medium
ordinal: 34000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The dash client itself (ADR-0023): cmd/sextant-dash connects under a bus identity + display_name and assembles the surfaces via the layout engine into the cockpit default; a thin 'sextant dash' alias execs it. Forkable, no special privilege — just another client over the SDK (ADR-0014).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 cmd/sextant-dash connects under a bus identity + display_name; thin 'sextant dash' alias
- [ ] #2 cockpit default assembles presence + message-stream + artifact; detail-on-demand; panes toggle/swap
- [ ] #3 e2e: launch, see presence + live stream, send a message, open an artifact; recorded as a VHS `.tape`, PTY-verified, with the rendered `.gif` attached to the PR
<!-- AC:END -->
