---
id: TASK-132
title: macOS menu-bar attention indicator — glanceable 'needs you' across apps
status: To Do
assignee: []
created_date: '2026-06-16 21:12'
labels:
  - feature
  - menubar
  - dash
  - macos
  - ux
  - 'slug:feat-macos-menubar-attention-indicator'
  - P3
  - ready-for-human
dependencies: []
priority: low
ordinal: 122000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena's idea (2026-06-16, #outbox): a macOS top-menu-bar icon that shows whether anything needs her attention, so she can tell at a glance — without opening the dash — whether her judgement is wanted. Fits v0.5's north star (calm: only surface what needs the operator). Concrete first version = a sextant menu-bar app reflecting the same 'needs you' projection the dash Home computes (review-pending artifacts + waiting criteria + question-messages awaiting the operator): a calm icon when clear, an attention badge when something waits, click → open the dash at the inbox. Her broader framing ('from any application') is the aspiration — a unified cross-app attention indicator; sextant's is the concrete model to build first.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A macOS menu-bar (status item) shows a clear/calm state when nothing needs the operator and a distinct attention state when something does
- [ ] #2 The attention state is driven by the same needs-you projection as the dash (review-pending + waiting criteria + question-messages awaiting the operator) — projection, not a separate store
- [ ] #3 Clicking the menu-bar item opens the dash (or a small popover summarizing what needs you)
- [ ] #4 It updates live (subscribes/polls the local dash API or the bus) and stays calm/quiet otherwise
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: #outbox (2026-06-16), during the v0.5 AFK push. Number claimed via backlog.counter CAS (132). Design call (native Swift status-item app vs a cross-platform tray; how it reads needs-you — the dash local API /api/* + SSE, or a thin bus client) → ready-for-human. Aspiration: 'attention from ANY application' (a unified indicator) is broader/out-of-scope for v1 — sextant's needs-you indicator is the concrete deliverable. Related: the v0.5 inbox-as-projection model (goals-design / v0-5-charter), TASK-120 (one next action), the calm/only-surface-what-needs-you tenet.
<!-- SECTION:NOTES:END -->
