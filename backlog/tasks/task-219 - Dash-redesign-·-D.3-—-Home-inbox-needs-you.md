---
id: TASK-219
title: Dash redesign · D.3 — Home inbox (needs-you)
status: To Do
assignee: []
created_date: '2026-06-24 01:08'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-home-goals
dependencies:
  - TASK-217
  - TASK-208
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: high
ordinal: 209000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The front door: a single ranked column of what needs operator judgement now; everything calm and handled pushed below or hidden. Parent: EPIC D (task-201). Covers AC §3.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S3.1 opens with a time-of-day greeting + a one-line summary: count needing a decision - goals waiting of total - everything else is moving on its own
- [ ] #2 S3.2 Start here: the single most urgent brief as a hero card (type badge, read-effort, title, summary, what it unblocks, run ULID, goal, age, Open to decide)
- [ ] #3 S3.3 briefs ranked by urgency: change/re-review, then decision, then quick question, then capture; hero is rank 1, the rest under Then - N more
- [ ] #4 S3.4 the Then rows show index, type glyph, title, type badge, read-effort, originating goal, age; each opens its brief
- [ ] #5 S3.5 Goals - N need you: only goals with a waiting/blocked criterion, each with a criteria status bar, M-of-N met, rollup verdict chip; All goals link to the portfolio
- [ ] #6 S3.6 Moving on its own: in-progress runs tied to a goal with live pulse, label, code, goal, latest activity; labelled nothing needs you; rows peek into the run
- [ ] #7 S3.7 Finished while you were away: one collapsed reassurance bar (N finished - no input needed) expanding to a checklist; collapsed by default
- [ ] #8 S3.8 empty state: greeting + Nothing needs you - because nothing's running yet + Define your first goal / Build your first workflow + a the-bus-is-live note
- [ ] #9 S3.9 sections only render with content (no empty headers); content enters with a subtle staggered fade; respect prefers-reduced-motion
<!-- AC:END -->
