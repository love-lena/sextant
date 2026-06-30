---
id: TASK-302
title: Live dash ships design-iteration scaffolding (demo data + Tweaks panel)
status: To Do
assignee: []
created_date: '2026-06-30 00:06'
updated_date: '2026-06-30 00:07'
labels:
  - bug
  - dash
  - ui
  - release
  - 'slug:bug-dash-ships-demo-data-and-tweaks-panel'
  - P1
  - ready-for-agent
dependencies:
  - TASK-204
priority: high
ordinal: 231000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The shipped web-dash carries dev/reviewer-only scaffolding from the redesign that should never reach the operator build: (1) a hardcoded SNAPSHOT demo dataset (app.jsx:289) with fake goals/artifacts/agents, and (2) the Tweaks panel (app.jsx:1565) that toggles accent/sidebar/motion and a data-mode (snapshot/blank). The data-mode DEFAULTS to 'snapshot' (app.jsx:281), and goalsShown/artsShown/agentsShown (app.jsx:1206-1208) overlay the seed data into any view the LIVE bus leaves empty. CONSEQUENCE (hit live 2026-06-29): after the operator's real goals were cleared, the Goals page silently filled with the three demo fixtures ('Ship the dash redesign' / 'Distributed leaf nodes' / 'Onboard the helm assistant') — indistinguishable from real goals, on a dash showing 'live'. The /*EDITMODE-BEGIN*/ markers (app.jsx:27, tweaks-panel.jsx) are a live design-edit host hook (__edit_mode_set_keys rewrites the block on disk), NOT a production build-strip — so nothing ever removed this for shipping. An operator dash must show only real bus state.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The shipped dash renders ONLY live bus data — an empty bus shows empty views (no seed goals/artifacts/agents); SNAPSHOT and the snapshot/blank data-mode are gone from the operator build
- [ ] #2 No Tweaks panel in the operator dash; the chosen design values are baked as fixed constants (accent #4f9d68, sidebar left/paper/sections, live-pulse on) so there is no visual change
- [ ] #3 Verified live: with zero goals on the bus, the Goals page reads '0 goals' (or equivalent empty state), not the demo fixtures
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
app.jsx: delete SNAPSHOT (285-324), DATAMODE machinery (273-283) + dataMode/snapshotOn state, and the *Shown overlays (1206-1208) -> views read goalViews/artItems/agents directly. Remove the Tweaks panel (1565-1591) + useTweaks/setTweak; replace t.* reads (accent/sidePos/sideTone/sideNav/livePulse, ~17 in app.jsx + 1 in sidebar.jsx) with the baked constants. Delete tweaks-panel.jsx + the edit-mode host hooks. Decide separately whether the design-iteration tooling returns as a dev-only, never-shipped affordance.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Provenance: surfaced 2026-06-29 during a bus cleanup — deleting the real goals revealed the seed overlay. Operator call: live code must not contain demo data or a tweaks panel. Open design call (why ready-for-human): full removal vs. preserve the design-iteration tooling behind a dev-only flag. Related: dash-redesign EPIC work (TASK-201/220), [[feat-dash-home-html-artifact-shortcut]] (TASK-301).

RESOLVED (operator, 2026-06-29): the Tweaks panel + data-mode + SNAPSHOT are leftover Claude design-generation tooling that got moved into the repo by mistake (cf. sextant.synth.datamode.v1, __edit_mode_set_keys). Decision: TOTAL removal — no dev-flag, no preserved design-iteration affordance. Built originally by [[TASK-204]] (Status system / Tweaks / data-mode); this reverses the Tweaks+data-mode half of it. Now ready-for-agent (fully specified).
<!-- SECTION:NOTES:END -->
