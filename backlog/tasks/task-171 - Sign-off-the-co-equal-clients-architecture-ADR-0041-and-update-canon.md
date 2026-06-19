---
id: TASK-171
title: Sign off the co-equal-clients architecture (ADR-0041) and update canon
status: To Do
assignee: []
created_date: '2026-06-19 21:11'
labels:
  - decision
  - canon
  - adr
  - 'slug:feat-canon-co-equal-signoff'
  - P1
  - ready-for-human
dependencies: []
ordinal: 161000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Sign-off gate for the co-equal-clients refactor (PRD doc-2, ADR-0041): the protocol is the product; one Go bus implemented once; the client surface is co-equal across languages; conventions are lexicon-defined libraries verified by conformance; the tree is domain-first. Canon changes only through a human-signed-off merge.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 ADR-0041 flips proposed to accepted on a signed-off merge
- [ ] #2 CONTEXT.md gains terms: conformance suite, co-equal client, the conventions layer
- [ ] #3 ADR README index lists 0040 and 0041 (index drift fixed)
<!-- AC:END -->
