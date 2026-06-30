---
id: TASK-261
title: >-
  Work-engine: agent-mode review loop broken — reviewer cannot emit run.decision
  (P0 live wedge)
status: To Do
assignee: []
created_date: '2026-06-30 04:14'
labels:
  - workengine
  - agent-mode
  - bug
  - P1
  - needs-triage
  - 'slug:fix-agent-mode-reviewer-decision-emission'
dependencies: []
priority: high
ordinal: 247000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The resident reviewer pi worker cannot publish a run.decision message. The pi worker's sextant_publish and reply tools hard-map to msg.topic.*/DM subjects — there is no deterministic path for the worker to emit a record to the run.decision subject the coordinator polls. As a result, every agent-mode run wedges at the first reviewed step: the coordinator sends a run.review DM, waits for a run.decision on the run's decision subject, and waits forever.

TASK-242 is marked Done but is broken live. Its tests pass only because the test reviewer raw-publishes directly to the decision subject (bypassing the worker toolset entirely) — a textbook gate-the-prod-adapter false pass. The real reviewer worker, using only its available MCP tools, has no path to emit a run.decision that the coordinator will observe. Reference: [[feat-agent-mode-run-coordinator]] (TASK-242).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A live agent-mode run advances past the first reviewed step because the real reviewer pi worker (using only its standard MCP tool set — sextant_publish, reply, or equivalent) emits a run.decision that the coordinator observes and applies. Proof: a live agent-mode run reaching done with the reviewer's decisions (advance/redo-with-feedback/stop verb + reasoning) recorded on the run's activity trail. Flipper: operator (live agent-mode run). Fake-pass guard: must use the real worker toolset — a test stub or harness that raw-publishes to the decision subject on the worker's behalf does not satisfy this AC; the worker process itself must emit the decision record via its own tools.
- [ ] #2 The decision-emission path is exercised with EACH of the four verbs in the TASK-242 decision vocabulary (advance, redo-with-feedback, edit-then-advance, stop), not just advance. Proof: integration tests or a live run log showing all four verbs successfully processed by the coordinator. Flipper: mechanical integration test on a real bus. Fake-pass guard: a test that only exercises advance (the happy path) leaves the other three verbs untested on the real worker path.
- [ ] #3 The fix does not introduce a new tool or capability visible only in the test harness that is absent from the production worker environment. Proof: the decision-emission tool or protocol is present in the standard pi worker skill set (or is a standard MCP tool); the production dispatcher spawns workers with it. Flipper: mechanical (diff check: no test-only tool injection). Fake-pass guard: a tool added only to the test harness's worker env is the same gate-the-prod-adapter pattern this ticket is fixing.
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Root cause investigation: trace how the coordinator sends the run.review DM and what subject it polls for the run.decision. Then trace what tools the reviewer pi worker has available and whether any of them can publish to that exact subject. The fix is likely one of: (a) extend sextant_publish to allow publishing to a run.decision subject when the worker holds the relevant run identity; (b) add a dedicated run_decision tool to the pi worker skill set; (c) redesign the coordinator-reviewer protocol to use a tool-call reply rather than a separate bus subject. Option (c) may be the cleanest and avoid the subject-mapping problem entirely.
<!-- SECTION:NOTES:END -->
