---
id: TASK-37
title: dash-widgetgallery still narrates the two-level focus model
status: To Do
assignee: []
created_date: '2026-06-09 22:46'
updated_date: '2026-06-11 00:02'
labels:
  - ready-for-agent
dependencies: []
ordinal: 43000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
R5 review minor m2: cmd/dash-widgetgallery/main.go (lines ~10,51,120,161,175,215) + its tape still say 'two-level focus model', 'enter step in - esc out', and run an own selected/active mini-host. The widget stratum legitimately keeps three focus states (ADR-0026), but the narration should describe widget focus states, not the retired dash navigation. Reword host strings + tape, re-render gif (vhs + magick -coalesce to judge). Also same-family wording: 'a list you step into' at pkg/tui/layout/preset.go:10 and cmd/sextant-dash/main.go:20 — 'step into' is the retired verb; reword to the open-in-place language.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-06-10: verified still present post-#99 merge (4887258). Current locations: cmd/dash-widgetgallery/main.go:51 ('two-level focus model') and :215 ('enter step in · esc out'); cmd/sextant-dash/main.go:20 and pkg/tui/layout/preset.go:10 ('step into').
<!-- SECTION:NOTES:END -->
