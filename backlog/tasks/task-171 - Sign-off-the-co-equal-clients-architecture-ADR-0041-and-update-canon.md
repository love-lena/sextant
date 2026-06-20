---
id: TASK-171
title: Sign off the co-equal-clients architecture (ADR-0041) and update canon
status: Done
assignee: []
created_date: '2026-06-19 21:11'
updated_date: '2026-06-20 02:04'
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

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Co-equal-clients architecture signed off + canon coherent. AC#1: ADR-0041 accepted (PR #231, Lena's signed-off merge). AC#3: ADR README lists 0040 + 0041 (#231). AC#2: CONTEXT.md gains the 3 terms - Co-equal client, Conformance suite, Conventions layer - defining the now-real ADR-0041 vocabulary (co-equal Go+TS SDKs, the protocol/conformance vectors, conv/goals as a lexicon-defined library). Per the refined autonomy model, the CONTEXT terms land on m6 and get the final human sign-off at the m6->main merge (canon ⇔ signed-off).
<!-- SECTION:FINAL_SUMMARY:END -->
