---
id: TASK-88
title: 'Release: Homebrew formula-bump PR can''t be auto-opened (Actions lacks PR perm)'
status: To Do
assignee: []
created_date: '2026-06-13 05:28'
labels:
  - bug
  - release
  - homebrew
  - ci
  - 'slug:bug-release-formula-bump-pr-perm'
  - P3
  - ready-for-human
dependencies: []
priority: low
ordinal: 93000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The release workflow (.github/workflows/release.yml) builds + publishes the release and pushes the homebrew-bump-<tag> branch with the regenerated formula, but gh pr create then fails: 'GitHub Actions is not permitted to create or approve pull requests'. So the formula-bump PR must be opened + merged manually every release (hit on v0.3.0; v0.2.0's run also shows failure). Fix: enable repo setting Actions -> General -> 'Allow GitHub Actions to create and approve pull requests', OR set the HOMEBREW_BUMP_TOKEN PAT secret (its pushes also trigger CI so auto-merge fires).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 a v* tag push opens (ideally auto-merges) the Homebrew formula-bump PR with no manual step
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Hit cutting v0.3.0 (manually opened+merged #126; release + tarballs published fine). Relates to [[project_release_pipeline]] / TASK-59. Repo-admin fix (a setting toggle or a PAT secret) — ready-for-human.
<!-- SECTION:NOTES:END -->
