---
id: TASK-188
title: >-
  Register the web dash as a keep-alive component (sextant components start/stop
  dash)
status: Done
assignee: []
created_date: '2026-06-23 19:34'
updated_date: '2026-06-24 01:01'
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
Once the web dash is a standalone, stateless-at-rest binary ([[feat-dash-standalone-binary]], [[feat-dash-stateless-mint-on-demand]]), it can finally join the managed Registry that ADR-0040 deliberately kept it out of -- the exclusion was justified by 'it acts like a connected client', and a connect-to-mint-then-close server no longer does. Add it to components.Registry (components.go:71) alongside dispatcher/workflow/violet; `sextant components start dash` then installs its KeepAlive+RunAtLoad LaunchAgent so it stays up via launchd (NOT `sextant up`, which only starts the bus) and the operator NEVER types --serve again. The existing sextant components status|start|stop|restart [name|--all] machinery then Just Works: stop is a launchd bootout (service.go:188) that defeats KeepAlive respawn, start re-bootstraps+kickstarts+health-checks. The headless-dash identity model is settled by ADR-0047: dash.creds gets ONE narrow loopback-scoped capability to mint the OPERATOR's browser session (so the page still acts AS the operator per ADR-0044/m6), not a dash-id session — a trusted local credential broker, not principal impersonation. Note: because the server is stateless at rest, you do NOT stop prod to dev-test -- a second dev server on a different port ([[feat-dash-side-by-side-dev]]) runs side-by-side against the same live bus. The component is purely about keeping prod up; dev is additive.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 components.Registry has a dash entry (Name: "dash", Binary: "sextant-dash") whose Args closure — the shared `func(creds, store, recipe) []string` signature, recipe unused — returns `--creds <creds> --store <store> --state-file <$SEXTANT_HOME/dash.json>` (port defaults to 8765); `sextant components start dash` writes the KeepAlive+RunAtLoad LaunchAgent so the dash comes up and STAYS up via launchd with NO `--serve` ever typed. It is NOT brought up by `sextant up` (which only starts the bus)
- [ ] #2 sextant components status|start|stop|restart dash all work; stop boots out the launchd job (no KeepAlive respawn); start health-checks that the HTTP listener actually serves — read the url from $SEXTANT_HOME/dash.json, GET it, require HTTP 200 before reporting healthy (not just launchd "running")
- [ ] #3 First-run identity provisions dash.creds carrying ONLY the narrow loopback-scoped capability ADR-0047 defines — minting the OPERATOR's browser session (issuance-denied, TTL-bounded, under the operator's id) — and nothing more: no operator/issuer authority, no sx.hb subscription, so it runs clean with no perms violation. A bus test asserts dash.creds CAN mint an operator-id session and CANNOT clients.register / clients.retire / principal.set
- [ ] #4 sextant dash url reads the managed $SEXTANT_HOME/dash.json state file the component writes; the URL/port is stable across restarts so a bookmark keeps working
- [ ] #5 Side-by-side dev verified live: with the managed dash up, a second dev `sextant-dash --port 0 --ui <worktree>/clients/go/apps/internal/dashapi/web/app` (the sextant-dash BINARY, not `sextant dash --serve` — which no longer serves per feat-dash-standalone-binary) runs concurrently against the live bus on a free port without disturbing the managed one (two co-equal dash sessions appear on the bus)
- [ ] #6 Live operator-equivalence (the m6 requirement, ADR-0047): a browser opened against the managed dash acts AS the operator — a DM to/from the page routes through the operator's inbox (the routing the per-tab cred broke), confirming the minted session is operator-equivalent, not a dash-id session
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Add the Component entry (mirror violet/workflow; Binary sextant-dash, Args build --serve --state-file). Provision dash.creds at first start with the right scope. Default the state-file to $SEXTANT_HOME/dash.json under the components layer (already anticipated by flags.go). Confirm bootout/bootstrap lifecycle + health check on the HTTP port.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: design session 2026-06-23. ADR-0046 AND ADR-0047 were accepted + merged in PR #247, so the identity model is settled and this ticket is AFK once the two prior tickets land. Reverses the dash exclusion in components.go:69 / ADR-0040; the headless-dash mint authority is governed by ADR-0047 (loopback-scoped operator-session delegation). dash.creds scope ties into [[bug-creds-sx-hb-subscribe-perms]] (TASK-185). Related: [[feat-dash-standalone-binary]], [[feat-dash-stateless-mint-on-demand]], [[feat-dash-side-by-side-dev]].
<!-- SECTION:NOTES:END -->
