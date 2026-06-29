---
id: TASK-253
title: >-
  Coordinator content reads not all routed through the recorded seam (AC#3 fence
  gap)
status: To Do
assignee: []
created_date: '2026-06-29 23:20'
labels:
  - workengine
  - coordinator
  - test
  - P3
  - needs-triage
dependencies: []
priority: low
ordinal: 239000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Found verifying #300: the content-opacity AC#3 test fences co.getArtifact, but the coordinator can also read content via co.c.GetArtifact directly (e.g. checkpoint() main.go:600). A content read added through the direct client call keeps the AC#3 test GREEN. Pre-existing (predates #300) and currently benign (no work-step content read exists) — hardening.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 All coordinator artifact-CONTENT reads route through the single recorded getArtifact seam (or the recorder wraps the client), so the AC#3 content-opacity test fences EVERY content read, not just seam-routed ones. Proof: a content read added via co.c.GetArtifact directly makes the AC#3 test RED. Flipper: mechanical. Fake-pass guard: a test that only fences the named seam does not count.
<!-- AC:END -->
