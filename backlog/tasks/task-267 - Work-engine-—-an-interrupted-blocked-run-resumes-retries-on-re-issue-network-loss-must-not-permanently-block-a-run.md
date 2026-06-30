---
id: TASK-267
title: >-
  Work-engine — an interrupted/blocked run resumes/retries on re-issue (network
  loss must not permanently block a run)
status: To Do
assignee: []
created_date: '2026-06-30 18:41'
labels:
  - workengine
  - coordinator
  - reliability
  - P1
  - needs-triage
dependencies: []
priority: high
ordinal: 253000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
When the host loses network mid-run, a dispatched worker's model calls fail; the worker drains and publishes a step-done run.event with artifacts:0. The coordinator's proof gate (correctly, for a hollow step) marks the step failed and the RUN blocked. The bug (D16, found in a live hermetic e2e): that blocked state was TERMINAL — a re-published run.start was skipped as 'already blocked (idempotent replay)' (the TASK-259 idempotent guard, which correctly skips done/live-owned runs, ALSO skipped blocked ones). So a TRANSIENT network interruption PERMANENTLY blocked the run, with no resume or retry. That fails DoD: 'it is okay that a run hangs when the machine loses network, but the run must RESUME or be RETRIABLE when the connection is re-established.'

Fix: distinguish terminal-FINAL (done/cancelled — never re-run) from blocked (resumable). A new workflow.IsResumableRun(status) is true only for blocked. shouldAdopt no longer skips a blocked run; claimOwnership re-claims it under the SAME single-writer CAS (a resume race can't double-dispatch); adopt resets the failed step (waiting/blocked -> upcoming, clears its prior produced refs) and the run (blocked -> running); walk re-dispatches FRESH from the first non-done step (prior done steps skipped, their artifacts already attached and piped in). A re-dispatch correlates the step-done event with THIS attempt (clearStepDone drops the prior attempt's retained event), so a replayed hollow outcome can't complete the fresh step. Cross-link: [[task-259]] (the idempotent-replay guard this refines), ADR-0051 (the run executor; resumption section updated), ADR-0045 (drain-and-revive restart-survival).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A run BLOCKED by a transient interruption (a step reported done with artifacts:0 — the hollow/drained-worker case) is RESUMABLE: re-issuing it (operator re-publishes run.start for the run id) RE-ADOPTS the run (it is NOT skipped as 'already blocked'), the previously-failed step goes running again and is re-dispatched FRESH from the prior steps' attached artifacts, and the run continues to done. Proof: clients/coordinator TestResume_BlockedRunResumesOnReissue over the real bus harness drives a run to blocked then resumes it to done. Flipper: mechanical (the integration test) + operator (live: lose network mid-run, restore, re-issue). Fake-pass guard: the test must FIRST confirm the run actually reached blocked (not skip to the happy path) AND assert the failed work step was re-dispatched (work-step spawn count increases) — a test that only proves a clean run does not cover resume.
- [ ] #2 A DONE run is NEVER re-run on re-issue (resume must not re-run a completed run); a cancelled run stays cancelled. Proof: TestResume_DoneRunStillSkipsOnReissue + the done-skip assertion inside TestResume_BlockedRunResumesOnReissue: re-publishing run.start for a done run leaves spawns, owner, status, and envelope revision unchanged. Flipper: mechanical (the integration test). Fake-pass guard: assert the SPAWN COUNT is unchanged after the re-issue (not merely that status is still done) — an idempotent skip that nonetheless re-dispatched would be caught.
- [ ] #3 Re-adoption preserves the single-writer + CAS-on-owner discipline (TASK-259): a resume re-claims ownership via the envelope CAS, so two coordinators racing a resume cannot both drive the run. The existing TASK-259 adoption tests (publish-before-subscribe, crash-readopt, idempotent-replay-of-DONE, stale-no-envelope) stay green. Proof: go test ./clients/coordinator/ -race passes the full suite including adoption_test.go. Flipper: mechanical. Fake-pass guard: the readopt-after-crash and idempotent-replay-of-DONE tests must pass UNMODIFIED — a fix that only flipped blocked to adoptable without the CAS would regress them.
- [ ] #4 The status predicate is co-equal in Go and TS (the run contract is a shared convention): workflow.IsResumableRun (Go) and isResumableRun (TS) both return true only for blocked. Proof: conventions/workflow/go run_test + conventions/workflow/ts records.test ('isResumableRun is blocked-only'). Flipper: mechanical. Fake-pass guard: the TS test asserts done/cancelled/running/waiting are all NON-resumable, not just that blocked is resumable.
<!-- AC:END -->
