---
id: TASK-172
title: Bump clients/claude-code plugin.json version per release (stuck at 0.5.1)
status: To Do
assignee: []
created_date: '2026-06-19 01:47'
labels:
  - bug
  - release
  - plugin
  - 'slug:bug-plugin-version-not-bumped'
  - P2
  - ready-for-agent
dependencies: []
ordinal: 162000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Claude Code plugin's plugin.json version is stuck at 0.5.1 — not bumped for v0.5.2/v0.5.3. Skills/hooks/MCP-text reach the operator via 'claude plugin update' (separate channel from the brew binary), and a stale plugin version can stop the update from being detected (e.g. the v0.5.3 /live-verify-v053 skill not appearing). The release pipeline (or the cut) should bump the plugin version to track the release tag, like the Homebrew formula does.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 plugin.json version tracks the release (bumped on each tag, automated in release.yml/gen if possible)
- [ ] #2 claude plugin update surfaces the v0.5.3+ skills (live-verify-v053) on the operator's setup
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Found shipping v0.5.3 (live-verify skill delivery). For v0.5.4.
<!-- SECTION:NOTES:END -->
