---
id: TASK-191
title: 'Release: ship sextant-dash + sextant-tui via brew; verify live upgrade'
status: To Do
assignee: []
created_date: '2026-06-23 19:58'
updated_date: '2026-06-23 20:24'
labels:
  - feature
  - dash
  - release
  - homebrew
  - build
  - 'slug:feat-dash-release-packaging'
  - P1
  - ready-for-human
dependencies:
  - TASK-188
  - TASK-189
priority: high
ordinal: 181000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The dash epic is not OPERABLE until the new binaries reach the live setup. A new binary (sextant-dash) plus a renamed one (sextant-tui) must be added to the release artifact build (release.yml) and the Homebrew formula, then a real brew upgrade must be verified to land them. Cut as its own slice because (a) release tags + formula changes need a human sign-off (trusted-path; the classifier blocks bus-authorized production pushes), and (b) you cut ONE release after the whole chain ([[feat-dash-standalone-binary]] -> [[feat-dash-stateless-mint-on-demand]] -> [[feat-dash-managed-component]]) lands, not per ticket. 'ACs require end-to-end live operability': done = the managed dash component runs the new binary on the live bus after a brew upgrade.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 scripts/release.sh + .github/workflows/release.yml build + package both sextant-dash and sextant-tui into the release artifact(s) — the release.sh binary map gains the sextant-tui entry and keeps sextant-dash mapped to clients/go/apps/dash
- [ ] #2 The Homebrew formula (scripts/gen-formula.sh output) installs both binaries
- [ ] #3 A live brew upgrade lands the new binaries; `sextant components start dash` (NOT `sextant up`, which only starts the bus) brings up the managed dash component on the upgraded binary; the dash serves and the sx.hb violation is gone
- [ ] #4 Release tag pushed with Lena's sign-off (trusted-path)
- [ ] #5 Cut-over cleanup: any manually-launched dev/ad-hoc dash servers (the `sextant-dash` binary, or a legacy `sextant dash --serve` from before the split — e.g. the dev servers left on :8765/:57869) are stopped so the managed dash component is the SINGLE dash serving on the live setup (no stray servers holding loopback ports)
<!-- AC:END -->



## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
After feat-dash-managed-component merges: extend release.yml binary set + Homebrew formula to include sextant-dash + sextant-tui; tag (Lena signs off); verify a real brew upgrade on the live setup brings the managed dash up on v-next.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: design session 2026-06-23 (split out of feat-dash-standalone-binary so a release sign-off does not block the AFK build work). Honors the live-sextant-via-release discipline (ship via brew, tags need sign-off). Related: [[feat-dash-standalone-binary]], [[feat-dash-managed-component]].
<!-- SECTION:NOTES:END -->
