---
id: TASK-242
title: >-
  Work-engine: optional agent-mode run coordinator (long-lived per-run
  supervisor)
status: To Do
assignee: []
created_date: '2026-06-27 01:12'
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
- [ ] #1 A workflow/template can opt into agent mode (a flag); default runs stay on the programmatic executor (TASK-236) unchanged
- [ ] #2 In agent mode, one long-lived coordinator agent per run reviews each step's output and returns a decision from a FLAT-STEP-MODEL v1 vocabulary: advance | redo-with-feedback (re-dispatch the same step) | edit-then-advance (bounded fix-up) | stop. NO graph reshaping (branch/insert/skip) in v1
- [ ] #3 The programmatic shell remains the sole single-writer of the run envelope; the coordinator agent only emits decisions the shell applies (cooperative, not a second writer)
- [ ] #4 The coordinator agent is a wrapper for workers: substantive work is delegated to dispatched workers; agent edits are bounded to fix-ups (fundamentally-wrong output -> redo-with-feedback, not the agent authoring). Identity is ULID + function, never a persona
- [ ] #5 The coordinator agent's own reasoning streams to its agent.activity feed (TASK-235) like any agent — observable for free
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Layer on the base executor (TASK-236): add an agent-mode flag on the template/run; when set, the programmatic shell, at each step boundary, consults a resident long-lived coordinator-agent (kept alive across the run, revived per ADR-0045) and applies its decision record (advance/redo/edit/stop). Reuse the dispatcher to stand up the coordinator agent; reuse run.event-style signalling for its decisions. Strictly additive to the base contract.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Decided with Lena 2026-06-26 during the run-executor design. Continuity choice = long-lived agent per run (not per-step-boundary dispatch). Authority = flat step model v1 (gate/redo/edit/stop; defer DAG/branching). Base executor [[feat-run-executor-workflow-run-v1]] (TASK-236) ships first (programmatic, output-gated). Rest/liveness signal from [[feat-pi-rpc-work-stream-to-bus]] (TASK-235) agent.activity turn_end. First consumer = [[task-98]] agentic dev workflow (reframe as 'the first agent-mode workflow'). Cross-link [[feat-run-checkpoint-resume]] (TASK-225), [[feat-run-cancel-stop]] (TASK-226).
<!-- SECTION:NOTES:END -->
