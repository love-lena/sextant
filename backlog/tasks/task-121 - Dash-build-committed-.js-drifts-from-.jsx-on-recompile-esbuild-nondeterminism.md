---
id: TASK-121
title: >-
  Dash build: committed .js drifts from .jsx on recompile (esbuild
  nondeterminism)
status: To Do
assignee: []
created_date: '2026-06-16 00:04'
labels:
  - bug
  - dash
  - build
  - ci
  - 'slug:bug-dash-js-jsx-drift'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 114000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
build-dash-ui.sh recompiles .jsx → .js with esbuild, but the output varies cosmetically (parenthesization) between runs/versions. Because both .jsx (source) and .js (embedded artifact) are committed, every recompile produces phantom drift that has to be hand-reverted. This has bitten repeatedly: PR #140 was a dedicated drift-fix, and the drift recurred on #142's rebuild the same session. The tax is paid on every dash PR. Fix shape: either (a) pin esbuild to an exact version so output is deterministic, or (b) stop committing .js — generate it at build/embed time (go:generate or a build step before go:embed) so .jsx is the single source of truth. (b) is the durable fix.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A dash .jsx edit + standard build produces a .js with no cosmetic diff beyond the intended change (deterministic), OR .js is generated at build time and not committed
- [ ] #2 CI fails if committed .js is stale relative to .jsx (guards the drift), if .js stays committed
- [ ] #3 build-dash-ui.sh / the embed path documents which approach is canonical
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: orion's #142 rebuild, 2026-06-15. Related: [[feat-home-single-next-action]]. Caused PR #140 (drift-fix) + recurred immediately.
<!-- SECTION:NOTES:END -->
