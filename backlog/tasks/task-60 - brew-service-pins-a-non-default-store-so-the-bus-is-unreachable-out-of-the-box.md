---
id: TASK-60
title: >-
  brew service pins a non-default store, so the bus is unreachable out of the
  box
status: Done
assignee: []
created_date: '2026-06-12 07:03'
updated_date: '2026-06-12 17:44'
labels:
  - bug
  - install
  - homebrew
  - 'slug:bug-brew-service-store-mismatch'
  - P2
  - ready-for-agent
dependencies: []
ordinal: 66000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Homebrew formula's service runs 'sextant up --store /opt/homebrew/var/sextant', but the CLI and the plugin's MCP default to the per-user store (UserConfigDir/sextant/jetstream). So after 'brew install' + 'brew services start sextant', a bare 'sextant dash' (and the plugin) look in the default store, find no bus, and fail with 'no servers available' — the bus is up but unreachable unless you set $SEXTANT_STORE. Hit on the first brew dogfood 2026-06-12.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 the brew service runs the bus on sextant's default store (no --store override)
- [x] #2 after brew install + brew services start, 'sextant clients register --self' + 'sextant dash' connect with no $SEXTANT_STORE
- [x] #3 the plugin's MCP server finds the bus with no env
- [x] #4 gen-formula.sh regenerates the matching (no --store) service so release bumps don't reintroduce the override
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Drop --store var/sextant from the formula service (and gen-formula.sh) so 'sextant up' uses defaultStore() — the same store the CLI + MCP discover. A user LaunchAgent has $HOME so the default store resolves.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in the first brew dogfood (2026-06-12). Related: [[feat-homebrew-install]] (TASK-59). Fixed in the same PR.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Fixed in PR #111 (27ca0c7): the brew service drops --store so the bus uses the sextant default store, and gen-formula.sh regenerates the matching service. Bus reachable out of the box after brew install plus brew services start.
<!-- SECTION:FINAL_SUMMARY:END -->
