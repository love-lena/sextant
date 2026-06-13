---
id: TASK-78
title: 'Web dash: vendor Google Fonts for full offline'
status: To Do
assignee: []
created_date: '2026-06-13 03:34'
labels:
  - feature
  - dash
  - frontend
  - offline
  - 'slug:feat-dash-web-vendor-fonts'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 83000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
D2 (TASK-71) vendored its JS to drop the runtime CDN, but index.html still loads Hanken Grotesk / Space Grotesk / IBM Plex Mono / Newsreader from fonts.googleapis.com. Cosmetic + degrades to system fonts, but it is the last runtime CDN dep. Vendor the woff2 + an @font-face stylesheet under web/app/vendor so the dash is fully offline.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 index.html loads no external font CDN; fonts served from web/app/vendor (embedded)
- [ ] #2 the families/weights actually used are vendored; UI looks unchanged offline
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Follow-up from D2 [[feat-dash-web-ui-d2]] (TASK-71, ADR-0033). JS CDN dep already removed; this is the cosmetic font dep.
<!-- SECTION:NOTES:END -->
