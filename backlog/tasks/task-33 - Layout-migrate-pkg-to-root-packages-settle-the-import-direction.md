---
id: TASK-33
title: 'Layout: migrate pkg/ to root packages + settle the import direction'
status: To Do
assignee: []
created_date: '2026-06-09 18:01'
labels:
  - feature
  - layout
  - style
  - ready-for-human
  - 'slug:feat-layout-no-pkg'
  - P2
dependencies: []
priority: medium
ordinal: 39000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Go house style (go-house-style skill, adopted with TASK-17) bans the pkg/ directory: name packages for what they provide at the root, use internal/ for unimportable ones. The current tree predates the rule — pkg/{bus,conninfo,sextant,sx,wire} — and the import graph is bidirectional (pkg/bus and pkg/sextant import internal/{backend,wireapi}; internal/wireapi and internal/docgen import pkg/). Two decisions are needed before the mechanical move: (1) which packages are the public SDK surface (stay importable at the root) vs implementation detail (move under internal/); (2) the one-directional import ruleset between domain and infra layers. Once settled, move the packages, update import paths (docgen hardcodes pkg/sextant in loadSDKDoc), and populate depguard in .golangci.yml — it is deliberately left unconfigured until the layers are real (see docs/agents/go-static-checks.md). The rule was adopted strict rather than softened to match the tree; this ticket is the gap.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 No pkg/ directory: every package lives at the root (public surface) or under internal/ (everything else)
- [ ] #2 The public-vs-internal decision is recorded (ADR or CONTEXT.md note) — which packages external clients may import
- [ ] #3 depguard enabled in .golangci.yml with the settled layer ruleset; make check green
- [ ] #4 internal/docgen's SDK doc path follows the moved package
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: TASK-17 house-style adoption (worktree-go-house-style). Related: [[feat-adopt-golangci-lint]] (TASK-17). The house-style skill and docs/agents/go-static-checks.md both cross-link this slug.
<!-- SECTION:NOTES:END -->
