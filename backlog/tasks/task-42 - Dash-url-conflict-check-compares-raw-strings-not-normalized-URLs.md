---
id: TASK-42
title: 'Dash: --url conflict check compares raw strings, not normalized URLs'
status: To Do
assignee: []
created_date: '2026-06-10 21:36'
labels:
  - bug
  - dash
  - onboarding
  - 'slug:bug-dash-url-conflict-exact-compare'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 48000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
ensureIdentity's first-run guard (PR #99) refuses to self-enroll when --url differs from the discovery file's URL — but the comparison is exact string equality, so the same bus spelled differently (localhost vs 127.0.0.1, missing scheme, trailing slash) false-positives the conflict. Safe failure mode (loud, with correct guidance to drop --url), but a spurious refusal. Fix shape: normalize both sides via url.Parse (scheme default, host case, trailing slash; decide whether localhost≡127.0.0.1 is in scope) before comparing.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 --url nats://localhost:4222 against a discovery file recording nats://127.0.0.1:4222 (and trailing-slash / missing-scheme variants) does not trip the conflict guard
- [ ] #2 A genuinely different bus still fails loud before any state is written
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: PR #99 fold review (2026-06-10), finding 7. Guard introduced by fix fbcd536 in the same PR.
<!-- SECTION:NOTES:END -->
