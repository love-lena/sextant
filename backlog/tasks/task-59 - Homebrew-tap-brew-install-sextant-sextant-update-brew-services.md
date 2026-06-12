---
id: TASK-59
title: 'Homebrew tap: brew install sextant, sextant update, brew services'
status: In Progress
assignee: []
created_date: '2026-06-12 05:22'
updated_date: '2026-06-12 05:33'
labels:
  - feature
  - install
  - release
  - homebrew
  - 'slug:feat-homebrew-install'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 65000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The manual install is painful: three binaries (sextant, sextant-mcp, sextant-dash) installed by hand (go install ./cmd/... or install bin/*), and upgrading means re-downloading a tarball. A Homebrew tap makes install + upgrade one command each. Decision: the main repo IS the tap (no separate homebrew-* repo) — formula at Formula/sextant.rb, users 'brew tap love-lena/sextant <url>' then 'brew install sextant'. Binaries come from the existing prebuilt release tarballs (no compile in the formula). Plugin stays on the Claude Code marketplace, unchanged. New flow: brew install sextant (binaries) then claude plugin install sextant@sextant (plugin).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Formula/sextant.rb installs the v0.1.2 prebuilt binaries for darwin/linux x amd64/arm64 with correct per-platform sha256; ruby -c and brew style clean
- [x] #2 Formula defines a brew service that runs 'sextant up' against a stable store under var, keep_alive, logs under var/log
- [x] #3 sextant update subcommand runs 'brew update && brew upgrade love-lena/sextant/sextant'; clear fallback message when brew is absent or sextant wasn't brew-installed; unit test for dispatch + fallback
- [x] #4 README install section leads with Homebrew (tap -> install -> claude plugin install; brew services start sextant); go install + raw tarball kept as secondary
- [x] #5 release.yml regenerates Formula/sextant.rb (version + 4 sha256) on a v* tag, opens a bump PR to main, gh pr merge --auto --squash; degrades gracefully without a PAT (PR opened, operator merges)
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Hand-write Formula/sextant.rb matching the regenerator's output shape. Add cmd/sextant update.go + dispatch wiring + usage. Update README. Add a regenerate step + PR/auto-merge to release.yml with a documented HOMEBREW_BUMP_TOKEN PAT caveat.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: operator request. Builds on [[feat-release-tagged-artifacts]] (TASK-47, the prebuilt tarballs). Operator-update: after merge, run 'brew tap love-lena/sextant https://github.com/love-lena/sextant && brew install sextant'; configure a fine-grained PAT secret HOMEBREW_BUMP_TOKEN (Actions: read/write, Contents: read/write, Pull requests: read/write) for hands-off formula bumps.

Implemented on branch feat-homebrew-install. Formula validated: ruby -c, brew style, brew audit --strict all clean (no accepted warnings). Test-installed via a local-file tarball variant (the live URL 404s anonymously while the repo is private) — install/service/test blocks all work, sextant version prints, brew test passes, clean uninstall. KEY CAVEAT for human: while love-lena/sextant is PRIVATE, brew install cannot fetch the release tarballs (404 to unauthenticated curl); the formula works once the repo or its release assets are public. gen-formula.sh reproduces the handwritten formula byte-for-byte (first auto-bump = clean diff). make lint + make test green; gofumpt clean.
<!-- SECTION:NOTES:END -->
