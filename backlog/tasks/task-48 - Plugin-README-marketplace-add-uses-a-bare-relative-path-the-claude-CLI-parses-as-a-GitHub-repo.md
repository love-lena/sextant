---
id: TASK-48
title: >-
  Plugin README marketplace add uses a bare relative path the claude CLI parses
  as a GitHub repo
status: Done
assignee: []
created_date: '2026-06-11 03:41'
updated_date: '2026-06-11 03:42'
labels:
  - bug
  - docs
  - claude-code
  - 'slug:bug-plugin-readme-bare-marketplace-path'
  - P1
  - ready-for-agent
dependencies: []
priority: high
ordinal: 54000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena's cold test of the v0.1.0 quickstart failed at plugin setup: the plugin README says 'claude plugin marketplace add clients/claude-code', but the claude CLI treats a bare relative path as a GitHub owner/repo spec and tries to clone github.com/clients/claude-code.git (Not Found). Reproduced against the published v0.1.0 tarball. './clients/claude-code' (or an absolute path) works. Earlier validation passed because it used an absolute path — the README line was never run verbatim. Fix: prefix the path with ./ in clients/claude-code/README.md and note why. demo.sh already uses an absolute path.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Plugin README's marketplace add command works verbatim from both a clone root and an unpacked release root
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: Lena's v0.1.0 cold test (2026-06-10). Related: [[feat-release-artifacts-dogfood]] (task-47). The v0.1.0 tarball's embedded README keeps the broken line until the next release; root README quickstart is unaffected (no marketplace command there).
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Fixed in clients/claude-code/README.md: marketplace add path is now ./clients/claude-code with a comment explaining that a bare a/b parses as a GitHub repo. Validated verbatim with hermetic CLAUDE_CONFIG_DIRs from both an unpacked v0.1.0 release root and the clone root (marketplace add + plugin install both succeed). demo.sh was already safe (absolute path). The v0.1.0 tarball's embedded README retains the broken line until the next release.
<!-- SECTION:FINAL_SUMMARY:END -->
