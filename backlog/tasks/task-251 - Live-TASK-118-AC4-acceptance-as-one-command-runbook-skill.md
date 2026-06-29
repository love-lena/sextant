---
id: TASK-251
title: Live TASK-118 AC4 acceptance as one-command runbook/skill
status: To Do
assignee: []
created_date: '2026-06-29 21:19'
labels:
  - work-engine
  - security
  - tooling
  - P3
  - needs-triage
dependencies: []
priority: low
ordinal: 237000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
From TASK-118: AC4 (zero TCC popups on the launchd-managed dispatcher) is operator-gated live-verify. Package it as a one-command /skill or runbook so the operator reproduces a per-criterion popup-free PASS/FAIL, mirroring the live-verify-v053 pattern.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The operator runs ONE command and gets a per-criterion popup-free PASS/FAIL for the TASK-118 sandbox (base worker + a multi-step workflow, no 'sextant wants to access' prompts). Proof: operator runs it on the live launchd setup and reports the result. Flipper: operator. Fake-pass guard: a dev-shell run (grants already exist) does not count — must run on the launchd-managed dispatcher.
<!-- AC:END -->
