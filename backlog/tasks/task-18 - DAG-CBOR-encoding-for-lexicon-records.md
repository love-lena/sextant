---
id: TASK-18
title: DAG-CBOR encoding for lexicon records
status: To Do
assignee: []
created_date: '2026-06-04 03:27'
labels: []
milestone: Future
dependencies: []
references:
  - docs/adr/0016-artifacts-are-lexicon-records.md
  - 'https://atproto.com/specs/data-model'
priority: medium
ordinal: 18000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Records are AT-Proto data-model values (ADR-0016) carried as JSON on the wire today; inline `bytes` pay base64's ~33% expansion. Add DAG-CBOR as the canonical/compact encoding of the same data model: `bytes` ride as native byte strings (no expansion), and it is the on-ramp to content-addressing (CIDs). This is an encoding change, not a record-shape change — lexicons and schemas are untouched. Decide scope: artifact values, message records, or both; and whether JSON stays the interchange form with DAG-CBOR as canonical (the AT-Proto split).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Records round-trip JSON <-> DAG-CBOR losslessly (incl. bytes, cid-link)
- [ ] #2 Inline bytes carried natively under DAG-CBOR (no base64)
- [ ] #3 Decide + document which surfaces use which encoding
<!-- AC:END -->
