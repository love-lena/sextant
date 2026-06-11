---
id: TASK-38
title: Stream compose clears the draft before the publish is known to succeed
status: To Do
assignee: []
created_date: '2026-06-09 23:36'
updated_date: '2026-06-11 00:02'
labels:
  - ready-for-agent
dependencies: []
ordinal: 44000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Pre-existing (predates TASK-36; surfaced by its golden review): Enter trims the draft, calls SetValue("") immediately, then publishes (pkg/tui/surface/stream.go ~line 343). On a publish FAILURE the error footer shows but the draft is gone — the author retypes the whole message. Fix shape: keep the optimistic clear (round-trip merge means success is the common case, and an un-cleared compose would shadow the merge) but RESTORE the draft into the compose when publishedMsg carries an error for this owner. Same for the artifact review comment path.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-06-10: verified still present post-#99 merge (4887258). Locations moved: pkg/tui/surface/stream.go:487 and pkg/tui/surface/artifact.go:380 (SetValue("") before publish). The publishedMsg owner-tag plumbing the fix needs shipped in #99.
<!-- SECTION:NOTES:END -->
