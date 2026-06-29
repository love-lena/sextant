---
id: TASK-244
title: Work-engine steps don't pipe; work-step output isn't captured
status: To Do
assignee: []
created_date: '2026-06-29 02:42'
updated_date: '2026-06-29 20:56'
labels:
  - feature
  - workengine
  - coordinator
  - P2
  - needs-triage
  - 'slug:feat-workengine-step-piping-and-output-capture'
dependencies: []
priority: medium
ordinal: 231000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
In a multi-step run, each step is a fresh one-shot pi worker prompted only with the run objective — it never receives the previous step's output. Evidence: run 01KW8J2NNZZA844WA5GDGDTJW8 — step 1 ("opus writing the brief") and step 2 ("passes off to gpt5 to rewrite") each wrote a DIFFERENT poem from scratch; step 2 never saw step 1. Worse, work-step output is never captured: steps 1 and 2 produced no artifacts — their poems exist only in each worker's agent.activity stream and are otherwise lost. Only the brief step persisted anything. So a 'pipeline' template neither pipes nor preserves intermediate work.
<!-- SECTION:DESCRIPTION:END -->


## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: run-examination 2026-06-28. Relates to the run executor (task-236). Design: how step output threads to the next step's prompt while keeping content opaque to the substrate.
<!-- SECTION:NOTES:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Multi-step run pipes REAL output: step N's worker prompt carries a reference to step N-1's produced artifact(s) and the step-N worker demonstrably operates on that input. Proof: a live 2-step produce-then-rewrite run where step 1's deliverable carries a unique token and step 2's deliverable is a DERIVATIVE that transforms/quotes that token (visible in step 2's agent.activity), not a from-scratch redo. Flipper: integration test + operator. Fake-pass guard: falsely passes if step 2 merely runs after step 1 with output independent of it — step 1's token MUST appear transformed in step 2's deliverable; 'step 2 ran' is not enough.
- [ ] #2 Every work step's deliverable is captured as a DISTINCT durable artifact attached to the run; a step that produced output but attached zero artifacts FAILS the run (the 01KW8J2N hollow case). Proof: artifact_list on the run after a live multi-step run shows >=1 attached artifact PER work step holding that step's actual deliverable; plus a negative test where a work step yields output-but-no-artifact ends the run blocked/failed, not done. Flipper: mechanical (artifact_list) + test. Fake-pass guard: falsely passes if the single brief artifact is counted for the work steps — proof requires a distinct artifact per work step, not 'the run has some artifact'.
- [ ] #3 Content-opacity preserved: the coordinator threads ONLY artifact refs/metadata between steps and NEVER reads artifact content to thread it. Proof: code inspection of the prompt-assembly path + a test asserting the coordinator issues no content read (artifact_get on step output) on the thread path; the worker dereferences the ref itself. Flipper: mechanical. Fake-pass guard: falsely passes if the coordinator reads content 'to summarize for the next step' — the thread must pass refs the worker resolves, coordinator never fetching step-output content.
<!-- AC:END -->
