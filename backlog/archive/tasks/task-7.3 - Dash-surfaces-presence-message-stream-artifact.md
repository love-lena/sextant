---
id: TASK-7.3
title: 'Dash: surfaces (presence, message-stream, artifact)'
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
- [x] #1 a Surface contract the panes implement (size/focus, render, intents, id+title)
- [x] #2 presence + message-stream(+compose) + artifact(reader/review) surfaces, public SDK only
- [x] #3 each surface runs standalone and mounts as a pane unchanged; teatest goldens + a VHS `.tape`, PTY-verified; the rendered `.gif` attached to the PR
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented on feat/dash, PR #99 (https://github.com/love-lena/sextant/pull/99). All acceptance criteria met + verified via two-stage (spec + code-quality) review per subtask. Whole-module `go test ./...` green incl. the no-tag internal/dash e2e; PTY-verified in tmux. Status In Progress pending human sign-off (merge). Commits 54c38f8 + 8680340 + 20032af.

Fixed in: 4887258 (PR #99)
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in PR #99 (squash 4887258) as part of TASK-7.
<!-- SECTION:FINAL_SUMMARY:END -->
