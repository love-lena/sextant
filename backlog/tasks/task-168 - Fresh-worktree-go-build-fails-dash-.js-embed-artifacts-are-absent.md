---
id: TASK-168
title: 'Fresh-worktree go build fails: dash .js embed artifacts are absent'
status: To Do
assignee: []
created_date: '2026-06-18 22:45'
updated_date: '2026-06-19 21:42'
labels:
  - bug
  - dash
  - build
  - ergonomics
  - P3
  - needs-triage
  - 'slug:bug-dash-js-absence-fresh-worktree-build'
dependencies: []
priority: low
ordinal: 158000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A fresh git worktree (or clean clone) cannot `go build ./cmd/sextant` — or anything importing internal/dashapi — until the dash web assets are generated. The compiled internal/dashapi/web/app/*.js are esbuild artifacts: gitignored + generated (only the .jsx is committed, per TASK-121 [[bug-dash-js-jsx-drift]]). debug.go's //go:embed needs those .js, so a Go-only build fails with a cryptic 'pattern web/app/app.js: no matching files found'. CI works because the setup-node step runs the build first; it bites fresh agent worktrees doing Go-only verify (orion + canopus both hit it during v0.5.3). This is .js ABSENCE, distinct from TASK-121 which is .js DRIFT on recompile.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Running go build/test in a fresh worktree without first generating the dash assets gives a clear, actionable message (or a make target makes it unnecessary)
- [ ] #2 A documented one-step path: `make ui` (or `make test`, which depends on it; or `go generate ./internal/dashapi/`) generates the .js before any go build
- [ ] #3 Fresh-worktree / agent guidance notes the generate-then-build step
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Options (pick on triage): (a) a friendlier embed guard / build-time error pointing at `make ui`; (b) a make target that generates-then-builds; (c) document in the worktree/agent onboarding. Low-risk, ergonomics. The //go:generate directive already exists in debug.go.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in v0.5.3 (orion + canopus). Distinct from [[bug-dash-js-jsx-drift]] (TASK-121 = drift; this = absence). Fix path confirmed by sirius: `make ui` (or make test) before go build.

Dash backend model revised by ADR-0041 / task-179 / task-180: the /api/* + SSE + bearer-token + internal/dashapi mechanism described here no longer applies. Re-frame the surviving need against the direct TS NATS-WebSocket client.
<!-- SECTION:NOTES:END -->
