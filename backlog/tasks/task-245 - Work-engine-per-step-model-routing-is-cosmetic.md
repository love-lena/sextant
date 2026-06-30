---
id: TASK-245
title: Work-engine per-step model routing is cosmetic
status: Done
assignee: []
created_date: '2026-06-29 02:42'
updated_date: '2026-06-30 00:56'
labels:
  - feature
  - workengine
  - dispatcher
  - P3
  - needs-triage
  - 'slug:feat-workengine-per-step-model-routing'
dependencies: []
priority: low
ordinal: 232000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Step labels imply per-step models/providers (e.g. 'Starts with opus…', 'passes off to gpt5…') but every step runs as the dispatcher's default pi worker (claude-haiku-4-5). Nothing routes a model/provider per step. Evidence: run 01KW8J2NNZZA844WA5GDGDTJW8 — all three step workers were haiku pi. The template's model intent is fiction.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A template step that declares a model/provider dispatches a worker that ACTUALLY RUNS on it. Proof: a live run with a step declaring a non-default model; the worker's reported model id in its agent.activity/usage == the declared model. Flipper: mechanical (read the worker's actual model id from usage) + operator. Fake-pass guard: falsely passes if the label is cosmetic and the worker still runs the default — the declared model MUST differ from the default and the proof reads the ACTUAL model id, so a no-op (still haiku) fails.
- [ ] #2 A step with NO declared model runs the dispatcher's documented default model. Proof: a step without a model declaration -> worker's actual reported model id == the documented default. Flipper: mechanical. Fake-pass guard: falsely passes only if the default id is mis-read — proof reads the actual reported id.
- [ ] #3 A step declaring an UNSUPPORTED/unknown model FAILS LOUD at dispatch — no silent fallback to the default. Proof: a run with a bogus model declaration ends blocked/errored with a clear message AT SPAWN and NO worker is spawned on the default. Flipper: mechanical + operator. Fake-pass guard: falsely passes if dispatch silently falls back to haiku — the negative test asserts NO worker spawned + a loud error, not a quietly-defaulted run. (The original 'or the label is removed' escape is REMOVED: routing must work.)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: run-examination 2026-06-28. The pi recipe honors SX_AGENT_MODEL; the coordinator would need to pass a per-step model through the spawn.request → recipe.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Per-step model routing is now real, not cosmetic. Changes: (1) RunStep.Model field added to conventions/workflow/go/run.go and its TS peer run.ts; (2) SpawnRequest.Model added to both workflow-local mirror (records.go) and canonical spawn convention (conventions/spawn/go/records.go + TS spawn.ts); (3) coordinator/main.go passes step.Model through both runDispatch and runBrief SpawnRequests; (4) dispatcher/main.go adds resolveModel() gate (validates against SupportedModels before spawn), stores model on managedAgent, and sets SX_AGENT_MODEL in launchHarness env. Supported model set: claude-haiku-4-5 (default), claude-sonnet-4-5, claude-sonnet-4-5-20251001, claude-opus-4-5, claude-sonnet-4-6, claude-opus-4-8. Tests: clients/dispatcher/model_test.go covers AC#1 (declared model flows to SX_AGENT_MODEL), AC#2 (no model uses DefaultModel), AC#3 (unsupported model errors pre-spawn). All suites green under -race. TASK-255 filed in same PR.
<!-- SECTION:FINAL_SUMMARY:END -->
