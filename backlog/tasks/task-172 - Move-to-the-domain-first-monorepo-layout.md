---
id: TASK-172
title: Move to the domain-first monorepo layout
status: To Do
assignee: []
created_date: '2026-06-19 21:11'
labels:
  - feature
  - layout
  - refactor
  - 'slug:feat-layout-domain-first-tree'
  - P1
  - ready-for-agent
dependencies:
  - TASK-171
ordinal: 162000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Mechanical move to the domain-first layout (supersedes the lost feat-layout-no-pkg): top level becomes protocol/, bus/, clients/<language>/ - organised by what things are, not Go visibility. No top-level pkg/; internal/ nests locally; single Go module. Pure rename plus import-path rewrite, zero behavior change. Extend importcheck to the new edges. PRD doc-2, ADR-0041.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 tree is protocol/ + bus/ + clients/go/{sdk,conventions,apps}; no top-level pkg/
- [ ] #2 go build ./... and the full suite pass unchanged; the diff is renames + import paths only
- [ ] #3 importcheck enforces: a convention imports the SDK only (never the bus); the bus never imports clients
<!-- AC:END -->
