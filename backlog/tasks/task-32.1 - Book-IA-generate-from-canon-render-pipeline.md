---
id: TASK-32.1
title: Book IA + generate-from-canon render pipeline
status: To Do
assignee: []
created_date: '2026-06-08 22:41'
updated_date: '2026-06-08 22:50'
labels:
  - docs
  - 'slug:docs-mdbook-ia-render-pipeline'
  - P3
  - ready-for-agent
dependencies: []
parent_task_id: TASK-32
priority: medium
ordinal: 35000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Keystone of the core reference book (umbrella TASK-32). Locks the book's information architecture (SUMMARY.md) and builds the generate-from-canon pipeline: a go-generate step that renders protocol/*.json + 'go doc ./pkg/sextant' into markdown under docs/book/src/ before 'mdbook build', wired into 'make book' and the mdbook CI workflow. Decision (Lena 2026-06-08): generate from canon, never hand-write the reference — drift-proof, matches the conformance-test/schema-compat posture (ADR-0022). Unblocks 32.2/32.3/32.4.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 docs/book/src/SUMMARY.md committed with the locked IA: Introduction; Getting started; The protocol; The Go SDK; For implementers
- [ ] #2 A go-generate generator reads protocol/methods.json + lexicons/*.json and emits markdown under src/protocol/ — the JSON 'description' prose becomes rendered tables/sections, not raw JSON blocks
- [ ] #3 The same generator renders 'go doc ./pkg/sextant' into an SDK API-reference markdown page
- [ ] #4 Generated markdown is committed; the mdbook CI job re-runs go-generate and FAILS on any diff (the same up-to-date guard the TS codegen uses), so docs can never silently drift from protocol/ or the SDK
- [ ] #5 'make book' runs go-generate then 'mdbook build' green; the mdbook CI job runs the generator before building so generated pages are never stale
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Umbrella: [[docs-mdbook-protocol-reference]] (32.2), [[docs-mdbook-sdk-reference]] (32.3), [[docs-mdbook-implementer-pages]] (32.4) depend on this. Refs: ADR-0022 (locked core / generate posture), ADR-0017 (operation surface), ADR-0006 (frame), ADR-0020 (registry). The empty book + Pages deploy already shipped (PR #96, c2792dc); this fills it. 'src/ is canon' = authored prose + the generator; generated md is committed but owned by the generator.

PROSE/AGENT SPLIT (Lena 2026-06-08): agent owns the generator, the go-doc render, the CI drift-check, and make/CI wiring — NOT prose. The IA (SUMMARY.md) and all prose stubs already landed on branch docs-mdbook-scope (this scoping PR): Introduction, getting-started, protocol Overview + Connection, SDK Overview, Wire API — each carries a 'Claude outline — TODO for Lena' banner and is Lena's to write. The generated pages are the empty draft slots in SUMMARY (Operations · Records & lexicons · The frame · Clients registry & presence · Epoch · SDK Messages/Artifacts/Clients · API reference · backend interface). AC#1 (SUMMARY w/ locked IA) is satisfied by this PR; agent verifies + builds on it.
<!-- SECTION:NOTES:END -->
