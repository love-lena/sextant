---
id: TASK-181
title: Revive the Go house-style skill and a curated static-checks gate
status: In Progress
assignee: []
created_date: '2026-06-19 21:11'
updated_date: '2026-06-19 22:56'
labels:
  - feature
  - tooling
  - lint
  - go
  - 'slug:feat-go-house-style-static-checks'
  - P3
  - ready-for-agent
dependencies:
  - TASK-172
ordinal: 171000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Revive the Go house-style skill (the judgment layer) with its real rationale - interpretability / tree-as-architecture / deep modules - and add a curated static-checks gate (a high-value golangci subset, not the kitchen sink; realises the long-open task-17). The skill documents the conventions the refactor commits to; the gate enforces the mechanizable ones. PRD doc-2.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 the go-house-style skill is restored and adapted to the new tree, framed by the tree-as-architecture rationale
- [ ] #2 a curated static-checks gate runs locally + in CI (e.g. errcheck, errorlint, containedctx, gochecknoglobals, the import check)
- [ ] #3 task-17 is reconciled as this ticket's realisation
- [ ] #4 the full tree passes the curated linter set clean in CI with zero //nolint debt; the gate runs in make lint + the CI Go job
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Inherited from parked TASK-17 (PR #100): 5 calibration decisions need an operator call before the gate is finalised - (1) containedctx exclusion for TUI model pkgs, (2) gochecknoglobals allowance for immutable lookup tables, (3) no-new-pkg vs pkg/tui rule, (4) error-wrapping policy reword, (5) test-file exclusions. Skill-revival half is AFK; this calibration half is ready-for-human - surface for the operator, do not guess.
<!-- SECTION:NOTES:END -->
