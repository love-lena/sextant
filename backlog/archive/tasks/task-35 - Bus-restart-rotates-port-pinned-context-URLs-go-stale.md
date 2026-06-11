---
id: TASK-35
title: Bus restart rotates port; pinned context URLs go stale
status: Done
assignee: []
created_date: '2026-06-09 19:23'
updated_date: '2026-06-10 23:58'
labels:
  - needs-triage
dependencies: []
ordinal: 41000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Hit live 2026-06-09 during the ADR-0024 dash demo: 'sextant up' restarted on a new ephemeral port (63605→50605); every enrolled context pins the enrollment-time URL, which wins over store discovery (ADR-0021 precedence), so all clients fail with 'nats: no servers available' until contexts are hand-edited or --url is passed. This breaks the zero-config dash story on the second day: first run enrolls + pins, bus restart strands it. Candidate fixes: (a) 'sextant up' reuses its previous port from the store's bus.json when free — stable address, no resolution change (preferred, smaller); (b) a LOCAL store's live discovery file beats a stale context URL — resolution-order change to ADR-0021. Decide shape before implementing.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented on feat/dash @ 6759f47 (shape (a), stable port): bus.Start resolves the port via stablePort — reuses the bus.json-recorded port when free, loud stderr notice + ephemeral fallback when taken, fresh store unchanged, explicit --port wins. ADR-0025 (proposed) + 3 tests in pkg/bus/restart_test.go. Verified live: two boots of the same temp store both came up on the same port. Closes with PR #99 merge.

Fixed in: 4887258 (PR #99)
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in PR #99 (squash 4887258): the bus keeps a stable address across restarts of a store (ADR-0025); restart falls back loudly when the port is squatted, notice now routed through Config.Logf.
<!-- SECTION:FINAL_SUMMARY:END -->
