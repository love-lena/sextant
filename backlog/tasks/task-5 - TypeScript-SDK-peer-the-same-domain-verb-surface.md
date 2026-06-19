---
id: TASK-5
title: 'TypeScript SDK peer: the same domain-verb surface'
status: Done
assignee: []
created_date: '2026-06-03 01:12'
updated_date: '2026-06-19 21:42'
labels:
  - superseded
milestone: Future
dependencies: []
references:
  - docs/adr/0008-clients-are-processes.md
  - docs/adr/0013-multi-backend.md
priority: medium
ordinal: 5000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TS SDK is a peer to the Go SDK with the same domain-verb API. Governed by ADR-0008, ADR-0013.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-06-10: the peer surface grew in #99 — artifact.list, and the ADR-0027 reconnect contract (relay generations, per-resume sub-id rotation, monotonic cursor, two-tier resume failure with ErrResumeDeferred). A TS peer must implement the same contract; ADR-0027 is the spec.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Superseded by task-174 (primitive TS Wire client) + task-175 (TS goals convention) under ADR-0041: the SDK is a primitive Wire client and conventions are a separate layer, so the TS-SDK-as-domain-verb-peer framing here is replaced.
<!-- SECTION:FINAL_SUMMARY:END -->
