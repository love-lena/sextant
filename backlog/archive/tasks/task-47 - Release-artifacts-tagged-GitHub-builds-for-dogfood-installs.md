---
id: TASK-47
title: 'Release artifacts: tagged GitHub builds for dogfood installs'
status: Done
assignee: []
created_date: '2026-06-11 02:59'
updated_date: '2026-06-11 03:32'
labels:
  - feature
  - release
  - build
  - dx
  - 'slug:feat-release-artifacts-pipeline'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 53000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Dogfood installs currently track whatever branch last ran go install — a broken WIP build can take down the bus/dash in daily use, and bugs found while dogfooding map to 'whatever was on the machine' instead of a version. The deliverable: pushing a v* tag publishes a GitHub release whose tarball is a complete, versioned install — the three binaries plus the Claude Code plugin directory — so the dogfood machine upgrades intentionally, by release, never by branch state. Side benefit: a pinned dogfood client talking to dev builds on the same bus exercises wire.Epoch additivity in daily use.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Pushing a tag matching v* triggers a GitHub Actions workflow that publishes a release with tarballs for darwin/arm64, darwin/amd64, linux/amd64, linux/arm64; each tarball contains sextant, sextant-dash, sextant-mcp, and the clients/claude-code/ plugin directory
- [x] #2 sextant --version, sextant-dash --version, and sextant-mcp --version print the tag and commit; sextant-mcp reports the same string as its MCP Implementation version (replacing the hardcoded 0.1.0)
- [x] #3 Dev builds still work unchanged: go install ./cmd/... succeeds with no release machinery and reports version 'dev'
- [x] #4 From an unpacked tarball with binaries on PATH and no Go toolchain, the plugin README install steps work verbatim: claude plugin marketplace add <unpacked>/clients/claude-code, plugin install, register --self, working session
- [x] #5 Root README documents the install-from-release path beside the from-source quickstart, including the upgrade one-liner (gh release download or equivalent)
- [x] #6 v0.1.0 is published from main via the workflow and its tarball passes the unpacked-install check above
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Version stamping: a single internal/version package with var Version = "dev", set via -ldflags -X at release build; all three cmds read it (CLI --version flag; sextant-mcp Implementation.Version). Workflow: .github/workflows/release.yml on tag push v*; matrix or loop of GOOS/GOARCH cross-compiles (module is CGO-free), assemble tarballs named sextant_<tag>_<os>_<arch>.tar.gz containing bin/ + clients/claude-code/, gh release create with generated notes. Plain go build + tar is fine; goreleaser only if it stays simpler than the script. Validation: unpack into a temp dir, PATH-prefix it, drive the plugin README steps against a throwaway bus (demo.sh shows the recipe). Decisions pinned: semver v0.x tags, no homebrew tap yet, no signing/notarization yet (file follow-ups if wanted). Final v0.1.0 tag push happens after the PR merges.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: TASK-22 dogfood kickoff — pinning the daily-driver install to releases instead of the active branch. Related: [[feat-claude-code-plugin]] (the plugin dir ships inside the tarball so the skill/manifest pin with the binaries). Operator step: switching the dogfood machine to the release (marketplace re-point + PATH) is intentionally not an AC — documented in the README instead.

Implementation on feat/task-47-release-artifacts (3cb3226). Local validation 2026-06-10: scripts/release.sh built all four tarballs; darwin/arm64 unpacked into /tmp with PATH stripped of the Go toolchain — sextant/sextant-dash/sextant-mcp all report the stamped tag + commit; bus up + register --self + publish + read all pass from the tarball binaries; plugin installed from the unpacked clients/claude-code into a hermetic CLAUDE_CONFIG_DIR (marketplace add + install OK); release.sh stamps the plugin manifest version with the tag. AC1/AC6 (live workflow run + v0.1.0) intentionally post-merge — the classifier correctly blocked a pre-merge rc tag push since it would publish a release. Gates: make lint, make test (-race), e2e all green.

PR #105 squash-merged to main as f6252e0 (CI green). AC1/AC6 blocked on the v0.1.0 tag push: the permission classifier requires explicit operator direction to publish a release. One command cuts it: git tag v0.1.0 f6252e0 && git push origin v0.1.0 — the workflow then builds, smokes, and publishes; validate the published tarball with the README's gh release download line + the unpacked-install check from the PR body.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped on PR #105 (squash f6252e0) + release v0.1.0. Tag push triggered .github/workflows/release.yml: four tarballs (darwin/linux × arm64/amd64) published, each carrying bin/{sextant,sextant-dash,sextant-mcp} and clients/claude-code/ with the manifest version stamped. Published-artifact validation (2026-06-10): README's exact 'gh release download | tar -xz' line, no Go toolchain on PATH, hermetic store/home/Claude config — all three binaries report v0.1.0 (f6252e0a50ec); bus up → register --self → publish → read pass on tarball binaries; plugin marketplace add + install succeed from the unpacked release. internal/version stamps releases via ldflags; dev builds report 'dev (<commit>)'. README quickstart documents release-first install beside from-source. Dogfood upgrades are now by release, never branch state.
<!-- SECTION:FINAL_SUMMARY:END -->
