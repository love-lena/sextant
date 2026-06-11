---
id: TASK-17
title: Adopt golangci-lint on the rebuild
status: To Do
assignee: []
created_date: '2026-06-03 22:59'
updated_date: '2026-06-11 00:02'
labels: []
milestone: Future
dependencies: []
references:
  - Makefile
  - .github/workflows/ci.yml
priority: medium
ordinal: 17000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The rebuild's Go quality gate is only `go vet` + a gofumpt formatting check (Makefile `lint`, CI `lint + test (Go)`). The old build carries golangci-lint (with a ~26-issue backlog accrued by adopting it late against a large tree). Adopt a curated linter set NOW while the rebuild is small (~600 LoC) so the tree stays clean by construction.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 `.golangci.yml` with a curated set (e.g. govet, staticcheck, errcheck, ineffassign, unconvert, errorlint, copyloopvar, gocritic) — not the kitchen-sink default
- [ ] #2 Wired into the Makefile `lint` target and the CI Go job
- [ ] #3 The rebuild tree passes the chosen set clean (no //nolint debt at adoption)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-06-10: the 'adopt while small' premise is eroding — #99 added ~15k lines (pkg/tui strata, dash). A golangci-lint v2 run during the #99 review found 5 findings to expect at adoption: internal/selfenroll/selfenroll.go:153, pkg/tui/widget/widget_test.go:117, pkg/tui/layout/model_test.go:124/178/444.
<!-- SECTION:NOTES:END -->
