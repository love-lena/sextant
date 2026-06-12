---
id: TASK-51
title: mdbook docgen drift not caught on protocol/SDK changes
status: To Do
assignee: []
created_date: '2026-06-11 05:03'
labels:
  - bug
  - docs
  - ci
  - docgen
  - mdbook
  - 'slug:bug-mdbook-docgen-drift-uncaught'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 57000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The mdbook workflow (.github/workflows/mdbook.yml) regenerates docs/book via 'go run ./cmd/docgen' and fails on drift in docs/book/src or docs/book/generated. But it is path-gated to 'docs/book/**' (and the workflow file), so edits to protocol/ or the Go SDK — which docgen CONSUMES to produce protocol/operations.md and sdk-go/* — never trigger the regen check. Result: committed generated docs silently drift from canon. Observed live on main HEAD 1d6ac58 (2026-06-10): 'go run ./cmd/docgen' dirties docs/book/src/protocol/operations.md and docs/book/src/sdk-go/{messages,reference}.md, i.e. the published docs are already stale. Surfaced while shipping TASK-50 (PR #107); reverted the drift there to keep that PR scoped.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 go run ./cmd/docgen produces no git diff on main (the current drift is regenerated and committed)
- [ ] #2 The docgen drift check runs (and can fail) when protocol/ or the Go SDK sources change, not only docs/book/** — e.g. broaden the workflow path filter to include protocol/** and the SDK, or drop the path-gate for the drift-check job
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Regenerate (make book / go run ./cmd/docgen) and commit the current docs/book drift. Then broaden mdbook.yml's pull_request/push path filter so the docgen-drift job also runs on protocol/** and pkg/sextant (SDK) changes — or split the drift-check into an unfiltered job and keep the Pages deploy path-gated to docs/book/**.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: TASK-50 / PR #107 (sextant-mcp identity). Related: [[reference_post_cutover_ci_surface]] notes the path-gating gap. The drift-check is the same up-to-date guard the TS codegen used to be; it just isn't wired to its real inputs.
<!-- SECTION:NOTES:END -->
