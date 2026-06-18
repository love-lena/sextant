---
id: TASK-169
title: >-
  Release-built nix flake (flake.nix in repo, bumped by the release pipeline
  like the brew formula)
status: To Do
assignee: []
created_date: '2026-06-18 23:25'
labels: []
dependencies: []
ordinal: 159000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
lena-2 (Ubuntu, nix flake/profile box) exposed that sextant has no maintained nix install. v0.5.2 leaf-connect was unblocked with a hand-pinned local flake.nix wrapping the v0.5.2 release tarball (hardcoded per-arch sha256), but that goes stale every release. Lena's call: the flake should be built from the release just like the Homebrew formula is auto-bumped (release.yml -> gen-formula.sh -> formula bump PR). Build a maintained flake.nix in the repo whose version + per-arch sha256 are regenerated on each v* tag (mirror the formula-bump path), so 'nix profile install github:love-lena/sextant' (or a flake input) stays current. The release binaries are static (CGO_ENABLED=0) so the flake can wrap the prebuilt binary (no buildGoModule/vendorHash/patchelf). Code change accepted; DEFER to a later release (v0.6+) per Lena. Adjacent: an auto-detecting install.sh (curl|sh) for the non-nix Linux path (the uname-m step is manual today). Interim artifact: lena-2-leaf-connect (the staged flake).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 flake.nix committed in the repo, exposes packages.{x86_64,aarch64}-linux.default = sextant
- [ ] #2 release pipeline regenerates the flake version + per-arch sha256 on each v* tag (like gen-formula.sh does for the brew formula); no hand-editing
- [ ] #3 nix profile install github:love-lena/sextant installs the current release; documented in the install docs
- [ ] #4 (stretch) auto-detecting install.sh for the non-nix Linux tarball path
<!-- AC:END -->
