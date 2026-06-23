---
id: TASK-188
title: >-
  Register the web dash as a keep-alive component (sextant components start/stop
  dash)
status: To Do
assignee: []
created_date: '2026-06-23 19:34'
updated_date: '2026-06-23 19:57'
labels:
  - feature
  - dash
  - components
  - launchd
  - creds
  - 'slug:feat-dash-managed-component'
  - P1
  - ready-for-agent
dependencies:
  - TASK-186
  - TASK-187
priority: high
ordinal: 178000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Once the web dash is a standalone, stateless-at-rest binary ([[feat-dash-standalone-binary]], [[feat-dash-stateless-mint-on-demand]]), it can finally join the managed Registry that ADR-0040 deliberately kept it out of -- the exclusion was justified by 'it acts like a connected client', and a connect-to-mint-then-close server no longer does. Add it to components.Registry (components.go:71) alongside dispatcher/workflow/violet so it comes up with the bus and the operator NEVER types --serve again. The existing sextant components status|start|stop|restart [name|--all] machinery then Just Works: stop is a launchd bootout (service.go:188) that defeats KeepAlive respawn, start re-bootstraps+kickstarts+health-checks. Note: because the server is stateless at rest, you do NOT stop prod to dev-test -- a second dev server on a different port ([[feat-dash-side-by-side-dev]]) runs side-by-side against the same live bus. The component is purely about keeping prod up; dev is additive.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 components.Registry has a dash entry (Name: dash, Binary: sextant-dash) with Args/port/state-file; the plist runs it; sextant up brings the dash up automatically
- [ ] #2 sextant components status|start|stop|restart dash all work; stop boots out the launchd job (no respawn); start health-checks the HTTP listener is actually serving
- [ ] #3 First-run identity mints a dash.creds scoped to allow clients.session minting (mint-on-behalf) and NOT requiring the sx.hb subscription — so the component runs clean with no perms violation
- [ ] #4 sextant dash url reads the managed dash.json state file written by the component; the URL/port is stable across restarts so a bookmark keeps working
- [ ] #5 Side-by-side dev verified live: with the managed dash up, a second 'sextant dash --serve --port 0 --ui <worktree>/web/app' runs concurrently against the live bus on a separate port without disturbing the managed one (two co-equal dash sessions appear on the bus)
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Add the Component entry (mirror violet/workflow; Binary sextant-dash, Args build --serve --state-file). Provision dash.creds at first start with the right scope. Default the state-file to $SEXTANT_HOME/dash.json under the components layer (already anticipated by flags.go). Confirm bootout/bootstrap lifecycle + health check on the HTTP port.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: design session 2026-06-23. GATED ON ADR-0046 acceptance + the two prior tickets. Reverses the dash exclusion in components.go:69 / ADR-0040. dash.creds scope ties into [[bug-creds-sx-hb-subscribe-perms]] (TASK-185). Related: [[feat-dash-standalone-binary]], [[feat-dash-stateless-mint-on-demand]], [[feat-dash-side-by-side-dev]].
<!-- SECTION:NOTES:END -->
