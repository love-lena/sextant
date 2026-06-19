---
id: TASK-181
title: Revive the Go house-style skill and a curated static-checks gate
status: To Do
assignee: []
created_date: '2026-06-19 21:11'
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
<!-- AC:END -->
