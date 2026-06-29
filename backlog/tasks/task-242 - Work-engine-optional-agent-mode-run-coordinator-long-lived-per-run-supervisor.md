---
id: TASK-242
title: >-
  Work-engine: optional agent-mode run coordinator (long-lived per-run
  supervisor)
status: To Do
assignee: []
created_date: '2026-06-27 01:12'
updated_date: '2026-06-29 21:16'
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
- [ ] #4 Observable: the coordinator-agent's turns/thinking/decisions stream to its agent.activity feed and render live in the dash — >=1 frame incl. turn_end per reviewed step. Proof: the dash shows the coordinator-agent's per-step activity during an agent-mode run. Fake-pass guard: a silent agent (no activity per reviewed step) fails.
- [ ] #5 Agent decisions do NOT bypass the deterministic floor (TASK-243): an agent advance / edit-then-advance still passes the proof-gate — a run cannot reach done over a declared deliverable that does not exist, even on 'advance'. Proof: an agent-mode run whose brief declares a phantom proof artifact ends BLOCKED despite an advance decision. Fake-pass guard: agent mode routing around the proof-gate fails.
- [ ] #6 edit-then-advance is UNBOUNDED by mechanism: the coordinator agent may directly edit ANY deliverable handed to it using its own judgment, and is expected to choose redo-with-feedback (re-dispatch to a worker) when rework is substantial. Proof: an integration run where the coordinator makes a fix-up via edit-then-advance AND a separate scenario where it chooses redo-with-feedback for a larger rework — its decision record shows which verb and why. Flipper: operator + integration. Fake-pass guard: the coordinator must RECORD which verb it used (no silent edit bypassing the decision/activity trail), and AC #3 single-writer-of-the-ENVELOPE still holds — editing deliverable artifacts is allowed, authoring run-ENVELOPE revisions is not. The earlier mechanical edit-size bound is REMOVED per operator decision 2026-06-29 (rely on coordinator judgment).
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Layer on the base executor (TASK-236): add an agent-mode flag on the template/run; when set, the programmatic shell, at each step boundary, consults a resident long-lived coordinator-agent (kept alive across the run, revived per ADR-0045) and applies its decision record (advance/redo/edit/stop). Reuse the dispatcher to stand up the coordinator agent; reuse run.event-style signalling for its decisions. Strictly additive to the base contract.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Decided with Lena 2026-06-26 during the run-executor design. Continuity choice = long-lived agent per run (not per-step-boundary dispatch). Authority = flat step model v1 (gate/redo/edit/stop; defer DAG/branching). Base executor [[feat-run-executor-workflow-run-v1]] (TASK-236) ships first (programmatic, output-gated). Rest/liveness signal from [[feat-pi-rpc-work-stream-to-bus]] (TASK-235) agent.activity turn_end. First consumer = [[task-98]] agentic dev workflow (reframe as 'the first agent-mode workflow'). Cross-link [[feat-run-checkpoint-resume]] (TASK-225), [[feat-run-cancel-stop]] (TASK-226).

Design decision 2026-06-29 (operator): edit-then-advance is UNBOUNDED — the coordinator may freely edit any deliverable handed to it and uses redo-with-feedback at its own judgment. Supersedes the 'edits bounded to fix-ups, never authoring' framing in the description. Single-writer applies to the run ENVELOPE only, not deliverable artifacts.

Content-opacity boundary (operator clarification 2026-06-29): 'decide from metadata, never parse the brief body' binds the PROGRAMMATIC/deterministic coordinator ONLY — the proof-gate + run-envelope single-writer, which must use typed run.event metadata and never read artifact content. The opt-in AGENT-MODE coordinator (TASK-242) is a convention CLIENT acting as an agent and DOES read produced content — judging acceptance / edit-vs-redo IS reading the deliverable. So TASK-243 AC2 constrains the deterministic gate, NOT the agent reviewer; PROBE B (content wrong but artifact exists) is the agent reviewer's job (TASK-242), not a deterministic-gate defect.
<!-- SECTION:NOTES:END -->
