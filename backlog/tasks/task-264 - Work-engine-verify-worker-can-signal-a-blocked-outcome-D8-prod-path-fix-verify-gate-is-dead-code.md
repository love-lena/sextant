---
id: TASK-264
title: >-
  Work-engine: verify worker can signal a blocked outcome (D8 prod-path fix;
  verify gate is dead code)
status: To Do
assignee: []
created_date: '2026-06-30 04:18'
labels:
  - workengine
  - bug
  - verify
  - P1
  - needs-triage
  - 'slug:fix-verify-worker-blocked-outcome-signal'
dependencies: []
priority: high
ordinal: 250000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
RunReporter.report() (clients/pi-bus/src/run_report.ts:152-154) hard-codes outcome:'done' — there is no tool and no code path for a verifier worker to emit outcome:'blocked'. The coordinator's runVerify gate checks `if ev.Outcome == workflow.RunBlocked` but this condition never fires on the real path: it only fires in PR #314's test fake (verifyDispatcher) which hand-sets the outcome directly. A real verifier worker concluding DoD-not-met still emits outcome:'done', so the run advances to done over a broken deliverable. D8 (independent verify step) is dead code without this fix — it only gates on a fabricated outcome, never on what an actual verifier says.

Root: the verificationCharter instructs the verifier to "report outcome=blocked in your run.event when DoD is not met" — but there is no sextant tool for the worker to latch a blocked outcome, and RunReporter has no latch path. Folds into PR #314 (branch feat/d8-verify-step) so D8's fix stays atomic. Cross-link: [[chore-ship-d7-d8-to-managed-released-path]] (TASK-263), [[task-98]].
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 (Northstar — property, adversarial) A verify step BLOCKS the run when its independent verifier concludes DoD-not-met: a run with a deliberately-broken deliverable (e.g. a code change that does not build) drives the run to `blocked` status, never `done`. This must exercise the REAL reporter→coordinator path: the verifier worker calls a worker-available mechanism, RunReporter emits outcome:'blocked', the coordinator's gate fires. Proof: a live or integration test run with a broken deliverable ends in run status `blocked` (not `done`), and the coordinator's runVerify gate is confirmed to have fired (log or test assertion). Flipper: operator or integration. Fake-pass guard: falsely passes if the only proof routes through verifyDispatcher's verifyOutcome hook (the test fake that hand-sets outcome) rather than the real tool→reporter path — the test must disable the fake dispatcher and use a real verifier worker.
- [ ] #2 A worker-callable mechanism latches a blocked outcome with a reason (e.g. a sextant_run_block MCP tool, registered only on a verify step). Proof: a unit test showing the tool sets a blocked latch on the RunReporter for the current step; the tool is absent from non-verify step workers (cannot be called by a work/plan/brief step). Flipper: mechanical unit test (RED→GREEN). Fake-pass guard: a tool added to all workers (not gated to verify steps) is overly broad — the latch must be step-kind scoped.
- [ ] #3 RunReporter emits the latched outcome: when a blocked latch is set, RunReporter.report() publishes outcome:'blocked'; when no latch is set, it publishes the default outcome:'done'. Proof: a run_report unit test with a latched reporter asserting outcome:'blocked' is published; a separate test with no latch asserting outcome:'done'. Flipper: mechanical unit test (RED→GREEN on the latched-blocked case, which currently fails because report() hardcodes 'done'). Fake-pass guard: a test that only asserts on the done path (no latch) does not cover the blocked path.
- [ ] #4 The verificationCharter (the verifier worker's skill/prompt) is updated to instruct the verifier to CALL the sextant_run_block tool (with a reason) when DoD is unmet — replacing the current instruction to "report outcome=blocked in your run.event" which is impossible with the existing toolset. Proof: the updated charter text references the tool by name; an integration run with a DoD-failing deliverable shows the verifier calling the tool in its activity trail. Flipper: operator (charter text + live run). Fake-pass guard: a charter that says "call sextant_run_block" but the tool is not in the worker's registered toolset fails — the tool must actually be available.
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Fix sites: (a) clients/pi-bus/src/run_report.ts — add a latch(outcome, reason) method and read it in report(); (b) clients/pi-bus — register a sextant_run_block MCP tool available on verify steps that calls reporter.latch('blocked', reason); (c) verificationCharter skill/prompt — update the instruction. These are contained within the pi-bus + coordinator packages and fold into PR #314 (feat/d8-verify-step). TASK-263 must not be treated as shippable until this fix lands in #314.
<!-- SECTION:NOTES:END -->
