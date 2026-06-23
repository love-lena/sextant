---
id: TASK-182
title: >-
  Remaining deep-module consolidations: cursor store and the SDK publish-output
  leak
status: Done
assignee: []
created_date: '2026-06-19 21:11'
updated_date: '2026-06-20 01:25'
labels:
  - feature
  - deep-modules
  - refactor
  - sdk
  - 'slug:feat-deep-module-cursor-publishoutput'
  - P3
  - ready-for-agent
dependencies:
  - TASK-172
ordinal: 172000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The remaining deep-module consolidations the new tree enables (from the assessment): extract the durable per-subject sequence-cursor as one core module (today re-implemented three times - mcp, violet, attest), and close the public-seam leak where the SDK returns the internal wireapi.PublishOutput type to callers. PRD doc-2.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 one cursor-store module owns the monotonic/atomic/idempotent advance; mcp/violet/attest become thin callers
- [ ] #2 the SDK publish path returns an exported value type; no caller imports internal/wireapi
- [ ] #3 behavior unchanged: the existing mcp/violet/attest resume tests pass against the shared cursor module, and importcheck forbids the three sites re-declaring their own cursor
- [ ] #4 importcheck asserts no package outside pkg/sextant imports internal/wireapi (the leak cannot reappear)
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Deep-module consolidations SHIPPED (PR #236 squash). (A) shared seqcursor module (clients/go/apps/internal/seqcursor: Open/Since/Advance/Retain/Save/Sanitize/Path, caller-owns-locking); mcp/substate + mcp/attest + violet/ack are thin callers, resume tests pass UNCHANGED. (B) SDK wireapi leak CLOSED: PublishMsg returns exported sdk.PublishResult{ID,Seq}; protocol/wireapi now imported only by SDK + bus. (C) importcheck rules, both applied to the real sets + non-vacuous: AssertUsesSeqCursor on the 3 cursor sites (TestCursorSitesDelegate - RED before refactor, GREEN now); AssertNoWireAtom on the client apps (TestAppsNoWireAtom + TestWireAtomRuleBites vacuity guard). Orchestrator independently re-ran the wireapi bite (injected wireapi into conv/goals -> TestAppsNoWireAtom FAILED as designed, reverted). Gate green (golangci 0, make test -race 32 ok, e2e). Behavior unchanged.
<!-- SECTION:FINAL_SUMMARY:END -->
