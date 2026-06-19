---
id: TASK-171
title: Run the dash as a managed component (sextant components) with --state-file
status: To Do
assignee: []
created_date: '2026-06-19 01:47'
labels:
  - feature
  - components
  - dash
  - 'slug:feat-dash-managed-component'
  - P2
  - ready-for-agent
dependencies: []
ordinal: 161000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
v0.5.3 ships the dash --state-file flag + sextant dash url, but the dash itself is still a manual run (nohup sextant dash --serve) — not registered in the components Registry, so it isn't KeepAlive-managed (dies on reboot) and a fresh setup's dash isn't started by components start --all. Add a dash entry to the components Registry that runs sextant dash --serve --state-file $SEXTANT_HOME/dash.json, so it's OS-managed like the runtimes and sextant dash url works out of the box.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 the dash is in the components Registry + comes up via sextant components start --all (+ start/stop/restart/status)
- [ ] #2 the dash runs with --state-file $SEXTANT_HOME/dash.json so sextant dash url works after a managed start
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Deferred from v0.5.3 S5; for v0.5.4. Operator asked it be ticketed.
<!-- SECTION:NOTES:END -->
