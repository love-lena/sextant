---
id: TASK-147
title: >-
  Dash: stream explorer — low-level click-through of messages + artifacts
  ('stats for nerds')
status: To Do
assignee: []
created_date: '2026-06-17 01:33'
labels:
  - feature
  - dash
  - explorer
  - observability
  - 'slug:feat-dash-stream-explorer'
  - P3
  - needs-triage
dependencies: []
priority: low
ordinal: 137000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena's outbox idea (2026-06-16): a dash view that lets you click around messages + artifacts at a LOW level — a 'stats for nerds' developer/inspector surface, distinct from the curated operator views (Home/inbox/review). Lets you inspect the raw bus: frames by subject (subject, author ULID, trust level, record JSON, seq), artifact records + revision history, subscriptions, presence. For debugging + curiosity. NOT in the locked v0.5.0 scope — a post-v0.5 feature; file + triage for a later milestone.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A dash surface browses raw messages by subject (frame metadata + decoded record), read-only
- [ ] #2 Browse raw artifact records + revision history at the record level
- [ ] #3 Lives as a separate 'explorer'/nerd view — never mixed into the curated Home/inbox (those stay calm + curated)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: Lena outbox 2026-06-16. Post-v0.5.0 (not in the locked press-release scope). Needs a design/scoping pass (what to surface, the UX). Related: the dash redesign.
<!-- SECTION:NOTES:END -->
