---
id: TASK-250
title: 'Dash has no test runner: no regression guard for workengine.jsx spawn logic'
status: To Do
assignee: []
created_date: '2026-06-29 21:15'
labels:
  - test
  - dash
  - work-engine
  - P3
  - needs-triage
dependencies: []
priority: low
ordinal: 236000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Discovered verifying TASK-248: the silent-base spawn race is fixed in source but clients/web-dash has NO test runner (plain JSX, no package.json, no specs), so the fix rests only on live inspection and a future refactor could silently reintroduce name-only resolution with nothing red to catch it.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A minimal JS/JSX test harness exists for clients/web-dash AND a test asserts the TASK-248 property: the Spawn form's chosen template survives a templates-array churn (transient find-by-name miss) and never silently falls back to base. Proof: the test goes RED if the templates[0] fallback is reintroduced into the resolution path; runs in CI. Flipper: mechanical (CI). Fake-pass guard: a test that only checks the happy path (no poll-churn/miss simulated) does not count.
<!-- AC:END -->
