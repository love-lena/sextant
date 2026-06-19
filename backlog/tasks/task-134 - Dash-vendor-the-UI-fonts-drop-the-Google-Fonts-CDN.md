---
id: TASK-134
title: 'Dash: vendor the UI fonts (drop the Google Fonts CDN)'
status: To Do
assignee: []
created_date: '2026-06-16 21:27'
labels:
  - chore
  - dash
  - no-cdn
  - v0.5
  - 'slug:chore-dash-vendor-fonts-no-cdn'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 124000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Flagged in pr-156-brief during the v0.5 reskin: the dash loads Hanken Grotesk + IBM Plex Mono from the Google Fonts CDN (pre-existing in index.html, unchanged by the reskin). The dash already vendors React/ReactDOM/marked to avoid a runtime CDN (D2 hardening, ADR-0034); fonts are the remaining CDN dependency. Vendor the woff2 locally (web/app/vendor or a fonts dir) + @font-face so the dash is fully offline/no-CDN (privacy + works without network). Small follow-up; should land before v0.5 ships.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Hanken Grotesk + IBM Plex Mono load from vendored local woff2, not the Google CDN
- [ ] #2 index.html has no external font CDN link; dash renders the fonts offline
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: pr-156-brief (v0.5 reskin stage a, 2026-06-16). Claimed via CAS (134). Candidate to fold into a Track-1 reskin stage. Holds the no-runtime-CDN discipline (ADR-0034). Related: v0-5-dash-design.
<!-- SECTION:NOTES:END -->
