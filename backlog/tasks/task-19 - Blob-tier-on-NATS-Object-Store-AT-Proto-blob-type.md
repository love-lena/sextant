---
id: TASK-19
title: Blob tier on NATS Object Store (AT-Proto blob type)
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
ordinal: 19000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
For large binaries, store bytes in a NATS JetStream Object Store (chunked, content-addressed by digest) instead of inlining them in a lexicon. Reference them from records via the AT-Proto `blob` type — {$type: blob, ref: {$link: <cid>}, mimeType, size}. Keeps lexicons small, avoids base64 entirely for big content, and seeds content-addressed lineage. The 'even better option' beyond inline bytes / DAG-CBOR.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A blobs API on the SDK (put/get) backed by JetStream Object Store
- [ ] #2 Blobs are content-addressed (digest/CID); refs resolve to bytes
- [ ] #3 Lexicons reference blobs via the AT-Proto blob type
<!-- AC:END -->
