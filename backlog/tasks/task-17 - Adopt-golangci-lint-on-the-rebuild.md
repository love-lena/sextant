---
id: TASK-17
title: Adopt golangci-lint on the rebuild
status: Done
assignee: []
created_date: '2026-06-03 22:59'
updated_date: '2026-06-19 21:42'
labels:
  - ready-for-human
  - superseded
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

WIP 2026-06-10 — paused, resume in a future session. PR #100 (branch `feat/go-house-style`, tip cc5f178) is open and awaiting Lena's sign-off; the branch is fully pushed, nothing uncommitted. State: gate (.golangci.yml + Makefile + CI) and skill adopted, all ACs ticked; skill trimmed 197→165 lines (6b58610); Codex review came back clean (zero Critical/Major); the two pre-existing minors it found are filed as task-34 ([[bug-bus-creds-chmod-window]]) and task-35 ([[bug-bus-relay-register-shutdown-race]]).

Calibration audit of the new rules against PR #99 (feat/dash, ~10k LOC TUI) — data only, no changes; resume items from it:
1. containedctx: 6/6 hits are the deliberate Bubble Tea pattern (Init/Update can't take ctx) — needs a decision: path exclusion for TUI model packages, sanctioned nolint idiom, or redesign mandate.
2. gochecknoglobals: 4/4 hits are immutable lookup tables (palettes, preset order) — add the config allowance go-static-checks.md already anticipates.
3. The skill's no-new-pkg/ rule vs pkg/tui: PR #99 adds five packages there per ADR-0023; either TASK-33 ([[feat-layout-no-pkg]]) explicitly owns the pkg/tui tree or the skill gets an interim 'TUI packages join the existing tree' line.
4. LOCKED wrapping policy ('libraries return root errors') contradicts the whole tree (pkg/bus wraps ~60×, dash wraps too) — reword toward 'libraries wrap for the call chain, cmd/ owns presentation' or file migration tickets.
5. Smaller: consider errorlint/unconvert in test-file exclusions; staticcheck QF1001 on tests is noise; godot/revive-exported produced zero noise. Skill is silent on Elm/Bubble Tea conventions, golden-test policy, and the imports_test.go layer-enforcement pattern (worth naming).
Also pending Lena's call: pre-commit hook runs full make check incl. -race; Codex suggests a fast subset (fmt/tidy/build/lint) since CI is authoritative.

2026-06-12 (canopus survey): reclassified In Progress -> To Do + ready-for-human. The work is parked (PR #100 open, awaiting Lena sign-off), not actively in progress. Unblocking needs the 5 calibration decisions above: containedctx exclusion for TUI model pkgs, gochecknoglobals allowance for immutable lookup tables, the pkg/tui no-new-pkg rule, the error-wrapping policy reword, and test-file exclusions.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Superseded by task-181 (revive Go house-style skill + curated static-checks gate), which realises this. The 5 open calibration decisions are carried onto task-181 (ready-for-human).
<!-- SECTION:FINAL_SUMMARY:END -->
