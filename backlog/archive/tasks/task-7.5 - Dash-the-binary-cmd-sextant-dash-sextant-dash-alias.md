---
id: TASK-7.5
title: 'Dash: the binary (cmd/sextant-dash + sextant dash alias)'
status: Done
assignee: []
created_date: '2026-06-06 03:00'
updated_date: '2026-06-10 23:58'
labels: []
milestone: 'M4: The dash (human UI)'
dependencies: []
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
- [x] #1 cmd/sextant-dash connects under a bus identity + display_name; thin 'sextant dash' alias
- [x] #2 cockpit default assembles presence + message-stream + artifact; detail-on-demand; panes toggle/swap
- [x] #3 e2e: launch, see presence + live stream, send a message, open an artifact; recorded as a VHS `.tape`, PTY-verified, with the rendered `.gif` attached to the PR
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented on feat/dash, PR #99 (https://github.com/love-lena/sextant/pull/99). All acceptance criteria met + verified via two-stage (spec + code-quality) review per subtask. Whole-module `go test ./...` green incl. the no-tag internal/dash e2e; PTY-verified in tmux. Status In Progress pending human sign-off (merge). Commits b78fee9 + cf2be18 + final-review fix d446944.

Fixed in: 4887258 (PR #99)
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in PR #99 (squash 4887258) as part of TASK-7.
<!-- SECTION:FINAL_SUMMARY:END -->
