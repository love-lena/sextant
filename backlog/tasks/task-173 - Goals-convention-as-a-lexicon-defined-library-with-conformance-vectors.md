---
id: TASK-173
title: 'Goals convention as a lexicon-defined library, with conformance vectors'
status: Done
assignee: []
created_date: '2026-06-19 21:11'
updated_date: '2026-06-20 01:00'
labels:
  - feature
  - conventions
  - goals
  - conformance
  - 'slug:feat-conv-goals-lexicon-library'
  - P1
  - ready-for-agent
dependencies:
  - TASK-172
  - TASK-183
ordinal: 163000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Establish the convention pattern end-to-end on goals: define the goal record + verb signatures in the lexicon; generate the Go record types; implement conv/goals collapsing the two hand-rolled halves (dash write half + violet read half) into one deep module - fixing the live label/state field-drift bug and the proof-filter scope divergence. Establish the conformance-vector format (recorded primitive-op transcripts) + runner; the Go suite replays them. PRD doc-2.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 goal record + verb signatures defined once in the lexicon; Go types generated from it
- [ ] #2 conv/goals is the single home for goal mechanics; the label/state bug and proof-filter divergence are gone
- [ ] #3 conformance vectors exist for the goal verbs and the Go suite passes them
- [ ] #4 the dash backend and violet consume conv/goals instead of hand-rolling goal logic
- [ ] #5 On the operator's live bus, the dash write path sets a goal criterion and violet's Home reads the same criterion's text+status with NO field-name fallback - a regression test sets a criterion via the dash path and asserts violet's reader sees identical text/status
- [ ] #6 the lexicon->Go type generation is built + documented (net-new: lexicons are read at runtime today) - a fresh agent knows where it lives and when it runs
- [ ] #7 after the swap, sextant dash --serve Goals and violet's curated Home render the same live goal identically
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Goals convention as a lexicon-defined library SHIPPED (PR #235 squash). Codegen (lexgen->goal_gen.go, //go:generate+make generate); conv/goals is the SINGLE home (generated types + SetCriterion + derived Rollup + proof-filter EffectiveStatus + Project read-model). label/state/title field-drift bug FIXED (violet fallbacks deleted, headline=northstar). dashapi WRITE + violet READ + dash RENDER (server-side GET /api/goals via goals.Project) all consume conv/goals -> proof rule in ONE place, dash & violet cannot diverge. Conformance vector replays via the 183 seam. Adversarial review caught 3 confirmed defects (dash render diverged on unproved-met + circular AC#7 test; AC#5 on a fake store; importcheck false-green) - all fixed + independently re-verified (importcheck bite: injected bus import -> guard FAILS as designed, reverted clean). Gate green (golangci 0, make test -race 31 ok, e2e ok). New exported surfaces recorded: goals.Ops, goals step sentinels (ErrGet/ErrUpdate/ErrPublish).
<!-- SECTION:FINAL_SUMMARY:END -->
