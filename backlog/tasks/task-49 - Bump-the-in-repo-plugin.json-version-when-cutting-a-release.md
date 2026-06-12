---
id: TASK-49
title: Bump the in-repo plugin.json version when cutting a release
status: To Do
assignee: []
created_date: '2026-06-11 04:01'
updated_date: '2026-06-12 17:45'
labels:
  - bug
  - release
  - claude-code
  - 'slug:bug-plugin-version-not-bumped-in-git'
  - P3
  - ready-for-human
dependencies: []
priority: low
ordinal: 55000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
With the repo-as-marketplace install (TASK-48), the in-repo clients/claude-code/.claude-plugin/plugin.json version string is now load-bearing: Claude Code applies plugin updates only when that string changes. release.sh stamps the version into the tarball copy, but git (what GitHub marketplace adds clone) keeps the hardcoded 0.1.0 — so a user pinned to a newer tag, or tracking main, may not receive updated plugin content until the string moves. Fix shape: either bump plugin.json as part of cutting a release (commit before tagging — could be enforced by the release workflow failing when the tag and plugin.json disagree), or drop the version field so the commit SHA becomes the version (every commit = new version; release.sh still stamps the tarball copy). Decide which and document it in the release process.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Installing or updating the plugin from a GitHub marketplace add pinned to a new tag yields that tag's plugin content, verified hermetically across two consecutive tags
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: TASK-48 self-review (2026-06-10). Related: [[bug-plugin-readme-bare-marketplace-path]], [[feat-release-artifacts-pipeline]]. Not user-visible yet: v0.1.1 is the first tag with the root marketplace manifest, so no one has crossed a tag boundary.

2026-06-12 (canopus survey): premise now live. v0.1.2 (#108) and v0.2.0 shipped, crossing tag boundaries, so the in-repo plugin.json version is load-bearing. #108 bumped it to 0.1.2, but the workflow-enforcement AC remains open -- needs the decision: enforce (release fails when the tag and plugin.json disagree) vs drop the version field (commit SHA becomes the version).
<!-- SECTION:NOTES:END -->
