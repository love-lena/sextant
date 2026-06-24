---
id: TASK-192
title: >-
  Dash redesign: adopt the design shell, nav & chrome (filler pages for new
  sections)
status: To Do
assignee: []
created_date: '2026-06-24 00:33'
updated_date: '2026-06-24 01:08'
labels:
  - ready-for-agent
  - lane-foundation
dependencies: []
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant.html
priority: high
ordinal: 182000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Adopt the Sextant design's app shell end-to-end. The left nav becomes Home · Goals · Work engine · Artifacts · Bus (the design's exact order + charcoal chrome); the floating Assistant button and the ⌘K palette chrome are present on every surface; and the new sections (Work engine, Bus) render as inert filler pages so the shell is complete and navigable before their features exist.

Build/iterate with `sextant-dash --port 0 --ui <worktree>/clients/go/apps/internal/dashapi/web/app` — serves the SPA from disk, no Go rebuild for UI-only changes, side-by-side with the prod dash on :8765 (ADR-0046). The browser is a direct NATS-WS co-equal client (ADR-0044). Visual target is the design files; verify against them.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Left nav shows Home, Goals, Work engine, Artifacts, Bus in that order with the design's charcoal chrome; the active row is marked.
- [ ] #2 Clicking a nav row navigates the stage and resets that root's back-stack; existing surfaces unregressed.
- [ ] #3 The floating Assistant button and the ⌘K palette chrome are present on every surface.
- [ ] #4 Work engine and Bus render as clearly-placeholder filler pages — navigable, no errors.
- [ ] #5 Sidebar collapse + persisted state preserved.
- [ ] #6 Matches the design files on visual review.
<!-- AC:END -->
