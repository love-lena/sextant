---
id: TASK-32
title: Write the core reference docs — the core module
status: To Do
assignee:
  - lena
created_date: '2026-06-08 18:41'
updated_date: '2026-06-08 22:50'
labels:
  - docs
dependencies: []
priority: medium
ordinal: 31000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Real human-facing reference for Sextant's core — the core module that ADR-0022 (mostly) locks and every other module builds over: the verb/operation surface (ADR-0017), the frame (ADR-0006), the clients-registry record shape + connection-derived presence (ADR-0020), and connection/auth/creds. This is the docs/book/ (mdbook) content AGENTS.md lists as forthcoming. Lena to write.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The verb/operation surface is documented as the protocol contract (operation names, record shapes, semantics)
- [ ] #2 The frame, the clients-registry/presence record shape, and connection/auth/creds are each documented
- [ ] #3 Lives in docs/book/ (mdbook) and is linked from AGENTS.md 'human reference + API'
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
SCOPED 2026-06-08 (umbrella). Covers the core protocol + the Go SDK; operator-guide and conventions (spawn/workflow) deferred as module docs. DECISION: generate the reference from canon (protocol/*.json + 'go doc'), never hand-write — drift-proof, matches conformance/schema-compat. IA (locked): Introduction · Getting started (install + first Go client) · The protocol (Overview · Operations · Records & lexicons · The frame · Clients registry & presence · Connection/auth/creds · Epoch) · The Go SDK (Overview · Messages · Artifacts · Clients & identity · API reference) · For implementers (Wire API · backend interface). Empty book + Pages deploy already shipped (PR #96, c2792dc). Breakdown: [[docs-mdbook-ia-render-pipeline]] (32.1, keystone) -> [[docs-mdbook-protocol-reference]] (32.2, closes AC#1+AC#2) · [[docs-mdbook-sdk-reference]] (32.3) · [[docs-mdbook-implementer-pages]] (32.4). 32.2-32.4 parallelize after 32.1. AC#3 (linked from AGENTS.md) lands with 32.1.

PROSE/AGENT SPLIT (Lena 2026-06-08): agents do generation/rendering/verifiable work only; Lena writes all narrative prose. The IA + prose stubs (each marked 'Claude outline — TODO for Lena') shipped in the scoping PR (branch docs-mdbook-scope). Generated pages are the empty draft slots in SUMMARY.md.
<!-- SECTION:NOTES:END -->
