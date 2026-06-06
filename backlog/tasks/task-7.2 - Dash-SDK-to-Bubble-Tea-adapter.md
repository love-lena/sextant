---
id: TASK-7.2
title: 'Dash: SDK to Bubble Tea adapter'
status: To Do
assignee: []
created_date: '2026-06-06 02:59'
labels: []
milestone: 'M4: The dash (human UI)'
dependencies:
  - TASK-3
  - TASK-4
references:
  - docs/adr/0021-the-dash-is-a-composable-pane-cockpit.md
  - docs/adr/0014-the-tui-is-a-client.md
parent_task_id: TASK-7
priority: medium
ordinal: 31000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The thin tea.Cmd adapter that bridges an SDK subscription into the Bubble Tea loop by re-yielding bus events as tea.Msg (ADR-0021; replaces the old Source/Pump, which stays collapsed into the SDK per ADR-0014). Round-trip merge: self-published messages return on the same subscription, no optimistic echo.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 a tea.Cmd subscribes via the SDK and re-yields each event as a tea.Msg
- [ ] #2 round-trip merge: a sent message arrives via the same subscription (no optimistic echo)
- [ ] #3 public SDK only, no bus/NATS types leak into the TUI; teardown cancels cleanly
<!-- AC:END -->
