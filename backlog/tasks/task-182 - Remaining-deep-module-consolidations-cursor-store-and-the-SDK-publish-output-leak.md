---
id: TASK-182
title: >-
  Remaining deep-module consolidations: cursor store and the SDK publish-output
  leak
status: To Do
assignee: []
created_date: '2026-06-19 21:11'
labels:
  - feature
  - deep-modules
  - refactor
  - sdk
  - 'slug:feat-deep-module-cursor-publishoutput'
  - P3
  - ready-for-agent
dependencies:
  - TASK-172
ordinal: 172000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The remaining deep-module consolidations the new tree enables (from the assessment): extract the durable per-subject sequence-cursor as one core module (today re-implemented three times - mcp, violet, attest), and close the public-seam leak where the SDK returns the internal wireapi.PublishOutput type to callers. PRD doc-2.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 one cursor-store module owns the monotonic/atomic/idempotent advance; mcp/violet/attest become thin callers
- [ ] #2 the SDK publish path returns an exported value type; no caller imports internal/wireapi
<!-- AC:END -->
