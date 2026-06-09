---
id: TASK-17
title: Adopt golangci-lint on the rebuild
status: In Progress
assignee: []
created_date: '2026-06-03 22:59'
updated_date: '2026-06-09 18:04'
labels:
  - feature
  - style
  - ci
  - 'slug:feat-adopt-golangci-lint'
  - P2
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
- [x] #1 `.golangci.yml` with a curated set (e.g. govet, staticcheck, errcheck, ineffassign, unconvert, errorlint, copyloopvar, gocritic) — not the kitchen-sink default
- [x] #2 Wired into the Makefile `lint` target and the CI Go job
- [x] #3 The rebuild tree passes the chosen set clean (no //nolint debt at adoption)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Picked up on worktree-go-house-style. Scope expanded per Lena: adopt a full Go house style — judgment-layer skill (.claude/skills/go-house-style) + enforced static-checks gate (golangci-lint config, Makefile gate, CI), adapted from her draft docs.

Adopted as the full two-layer house style: .golangci.yml (curated set: govet, staticcheck, errcheck+type-assertions, forcetypeassert, ineffassign, bodyclose, gosec, unconvert, copyloopvar, gochecknoinits, gochecknoglobals, errorlint, containedctx, noctx, revive subset, godot; gofumpt as formatter), make check gate (fmt/tidy/build/lint/go fix/govulncheck/race), CI runs the same targets, optional pre-commit via make hooks. Tree passes with zero //nolint. Real findings fixed along the way: unchecked Close on the creds-record write path (pkg/bus/auth.go), ctx-in-struct relay lifecycle refactored to a registry-of-cancels (pkg/bus), deprecated parser.ParseDir in docgen, go1.26.3->1.26.4 for two reachable stdlib vulns. depguard deferred to [[feat-layout-no-pkg]] (TASK-33). Judgment layer: .claude/skills/go-house-style; enforced layer doc: docs/agents/go-static-checks.md.

PR #100 (feat/go-house-style) opened for sign-off.
<!-- SECTION:NOTES:END -->
