---
id: TASK-242
title: >-
  Work-engine: optional agent-mode run coordinator (long-lived per-run
  supervisor)
status: To Do
assignee: []
created_date: '2026-06-27 01:12'
updated_date: '2026-06-30 00:20'
labels:
  - feature
  - workflow
  - work-engine
  - agent-mode
  - ready-for-human
  - 'slug:feat-agent-mode-run-coordinator'
  - P2
dependencies:
  - TASK-236
priority: medium
ordinal: 229000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The base run executor (TASK-236) is a programmatic, trusting state machine: it advances a run's steps mechanically, enforcing only output-on-done + liveness, never judging quality. Agent mode is the additive, opt-in alternative for quality-sensitive workflows (plan->build->review): a workflow can declare it runs under a LONG-LIVED coordinator agent — one per run, kept resident for context continuity — that reviews each step's output and decides what happens next. The programmatic shell still owns lifecycle and remains the SOLE writer of the run envelope (ADR-0048 single-writer); it delegates only the advance DECISION to the agent. The agent is ONLY a wrapper for the workers: it delegates real work to dispatched workers and its own edits are bounded to fix-ups, never authoring. Default stays programmatic; agent mode is opt-in per workflow/template. TASK-98 (LLM-orchestrator agentic dev workflow) becomes the first agent-mode workflow.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Opt-in: a template/run with the agent-mode flag runs under a coordinator-agent (decisions visible in its agent.activity); a run WITHOUT the flag spawns NO coordinator-agent and behaves identically to the programmatic executor (TASK-236). Proof: two live runs + a test asserting no agent spawned when off. Fake-pass guard: the agent-mode run shows >=1 coordinator decision the default lacks, AND the default run spawns no coordinator agent (clients.list).
- [ ] #2 Each decision verb DRIVES the run: advance->next step; redo-with-feedback->the SAME step re-dispatched with the agent's feedback IN the worker's prompt; edit-then-advance->a bounded fix-up then advance; stop->terminal. Proof: an integration run exercising each verb, incl. a forced wrong-output that actually loops via redo-with-feedback (same step id re-runs + feedback present). Fake-pass guard: the agent must not rubber-stamp (always advance) — the forced-wrong scenario loops; and the decision set is EXACTLY these four (no branch/insert/skip).
- [ ] #3 Single-writer preserved: every run-envelope artifact revision's author is the programmatic shell's client id; the coordinator-agent authors ZERO revisions (its decisions ride msg.* records, not artifact.update). Proof: a revision-authorship assertion. Fake-pass guard: the agent has no write path to the envelope at all, not merely 'did not write this time'.
- [ ] #4 edit-then-advance is UNBOUNDED by mechanism: the coordinator agent may directly edit ANY deliverable handed to it using its own judgment, and is expected to choose redo-with-feedback (re-dispatch to a worker) when rework is substantial. Proof: an integration run where the coordinator makes a fix-up via edit-then-advance AND a separate scenario where it chooses redo-with-feedback for a larger rework — its decision record shows which verb and why. Flipper: operator + integration. Fake-pass guard: the coordinator must RECORD which verb it used (no silent edit bypassing the decision/activity trail), and AC #3 single-writer-of-the-ENVELOPE still holds — editing deliverable artifacts is allowed, authoring run-ENVELOPE revisions is not. The earlier mechanical edit-size bound is REMOVED per operator decision 2026-06-29 (rely on coordinator judgment).
- [ ] #5 Catches the content-truthfulness gap TASK-243 (option A) intentionally routes here: a run whose brief DESCRIBES a deliverable in prose with NO corresponding typed produced-artifact (absent from RunEvent.Artifacts) is flagged by the agent reviewer, NOT advanced to done. Proof: an agent-mode run whose brief claims in prose an artifact that was never produced -> the reviewer returns redo-with-feedback or stop, never advance. Flipper: integration + operator. Fake-pass guard: the deterministic gate cannot catch this (no typed ref to existence-check) — only a reviewer reading content can; a reviewer that rubber-stamps the prose claim fails. This is the residual from TASK-243's metadata-only gate.
- [ ] #6 The coordinator agent delegates substantive work to dispatched workers (it is a wrapper for workers, never the author of a deliverable from scratch). It MAY freely edit any output handed to it — edit-then-advance is unbounded (Lena 2026-06-29) — and relies on its own judgment to choose redo-with-feedback when output is fundamentally wrong rather than fixable. Identity is ULID + function, never a persona.
- [ ] #7 Agent-mode decisions sit ON the TASK-243 deterministic proof-gate floor and can NEVER bypass it: an advance/stop-to-done decision over a step whose reported artifacts do not exist still FAILS the programmatic existence gate (verifyReportedArtifactsExist) — the agent cannot certify done over an absent or fabricated deliverable. Fake-pass guard: an agent that returns advance/done while the worker attached no artifact (or a name that doesn't resolve) must NOT reach run done; the deterministic gate rejects it. Flipper: mechanical test (RED->GREEN).
<!-- AC:END -->



## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Layer on the base executor (TASK-236): add an agent-mode flag on the template/run; when set, the programmatic shell, at each step boundary, consults a resident long-lived coordinator-agent (kept alive across the run, revived per ADR-0045) and applies its decision record (advance/redo/edit/stop). Reuse the dispatcher to stand up the coordinator agent; reuse run.event-style signalling for its decisions. Strictly additive to the base contract.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Decided with Lena 2026-06-26 during the run-executor design. Continuity choice = long-lived agent per run (not per-step-boundary dispatch). Authority = flat step model v1 (gate/redo/edit/stop; defer DAG/branching). Base executor [[feat-run-executor-workflow-run-v1]] (TASK-236) ships first (programmatic, output-gated). Rest/liveness signal from [[feat-pi-rpc-work-stream-to-bus]] (TASK-235) agent.activity turn_end. First consumer = [[task-98]] agentic dev workflow (reframe as 'the first agent-mode workflow'). Cross-link [[feat-run-checkpoint-resume]] (TASK-225), [[feat-run-cancel-stop]] (TASK-226).

Design decision 2026-06-29 (operator): edit-then-advance is UNBOUNDED — the coordinator may freely edit any deliverable handed to it and uses redo-with-feedback at its own judgment. Supersedes the 'edits bounded to fix-ups, never authoring' framing in the description. Single-writer applies to the run ENVELOPE only, not deliverable artifacts.

Residual from TASK-243 option A (2026-06-29): the deterministic gate decides only from typed produced-artifact metadata, so a brief that claims a deliverable ONLY in prose (no typed ref) cannot be caught deterministically. That content-truthfulness check is the agent reviewer's job — added as an AC above.
<!-- SECTION:NOTES:END -->
