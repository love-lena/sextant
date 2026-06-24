---
id: TASK-206
title: Dash redesign · B.2 — Composer (authoring)
status: To Do
assignee: []
created_date: '2026-06-24 01:08'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-review
dependencies:
  - TASK-192
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: medium
ordinal: 196000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
One reusable writing surface for goals, notes, and imported files. What the operator drafts is theirs until marked ready. Parent: EPIC B (task-199). Covers AC §16.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S16.1 opens in a seed shape: blank note, charter (North star / The vision / How we'll know it's done), or imported file (import banner name/type/size)
- [ ] #2 S16.2 has a kind tag, title input, prose sections per seed; byline is always You
- [ ] #3 S16.3 drafts autosave continuously (top bar shows autosaved-ago) and persist (sextant.synth.drafts.v1); visible only to the operator until ready
- [ ] #4 S16.4 mark-ready requires title + body; charter -> Mark ready & define (criteria §17), else Mark ready files an artifact + compose-done screen
- [ ] #5 S16.5 when a run prompted the writing, the rail shows the prompt, its questions, and an ask-a-question-back input routing a reply to the topic
- [ ] #6 S16.6 compose-done screen states the consequence (defined goal / filed artifact / ready draft) and reinforces nothing-visible-until-ready
<!-- AC:END -->
