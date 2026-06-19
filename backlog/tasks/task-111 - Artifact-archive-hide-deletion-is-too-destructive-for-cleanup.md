---
id: TASK-111
title: Artifact archive/hide — deletion is too destructive for cleanup
status: To Do
assignee: []
created_date: '2026-06-15 19:18'
labels:
  - feature
  - artifact
  - ux
  - 'slug:feat-artifact-archive-hide'
  - P2
  - ready-for-human
dependencies: []
priority: medium
ordinal: 106000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The only artifact lifecycle operation is delete (permanent). Operators want to clear old artifacts from the review/list views without destroying the record. 'Archive' (soft-delete, hidden from default views but recoverable) is the standard pattern. Discovered when sirius deleted 12 artifacts on lena's request to 'archive old ones' — permanently removed from the bus store, no recovery.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 artifact_archive marks an artifact hidden from default list/review views without deleting it
- [ ] #2 artifact_list has an --include-archived flag to show archived artifacts
- [ ] #3 artifact_unarchive restores a hidden artifact to the active set
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered 2026-06-15: sirius used artifact_delete for cleanup; lena expected a reversible archive. All deleted artifacts were stale (PR briefs + shipped research) so data loss was acceptable, but the pattern is wrong.
<!-- SECTION:NOTES:END -->
