---
id: TASK-236
title: >-
  Work-engine: no executor walks sextant.workflow.run/v1 runs through their
  steps
status: To Do
assignee: []
created_date: '2026-06-25 02:59'
updated_date: '2026-06-27 01:12'
labels:
  - feature
  - workflow
  - work-engine
  - dash
  - ready-for-human
  - 'slug:feat-run-executor-workflow-run-v1'
  - P1
dependencies:
  - TASK-235
priority: high
ordinal: 213000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
ADR-0048 shipped the run-record contract (TASK-193) and TASK-216 shipped the dash live-state VIEW, but nothing on the backend advances a run. The dash work-engine lane (workengine.jsx) is write-once: spawn does one createArtifact('workflow.run.<id>') + a chat seed (workengine.jsx:341-368), then only polls every 4s. Verified live: run 01KVYADZ4ET154VY7E5C4H54S2 sits at status:running / step s1 running with a single artifact revision and zero messages on sextant.workflow.>. No client subscribes to or mutates sextant.workflow.run/v1 records or msg.topic.run.*; an exhaustive grep finds the type only in the dash JS. So every run created from the dash is frozen at creation. Spawn also never publishes a start/dispatch signal (it writes the artifact directly), so a future executor must watch the artifact store or a new start subject. The old cmd/sextant-workflow engine consumes the OLD sextant.workflow/v1 + workflow.start contract and will never pick up workflow.run.* (disjoint type + namespace). One coordinator on the new contract closes this plus the checkpoint, cancel, activity-log, and stop-condition gaps.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A run spawned from the dash advances past step s1 (work-step running -> done, next -> running) driven by a backend coordinator, not the dash
- [ ] #2 The coordinator appends activity-log entries and attaches produced artifacts to the run record as steps complete
- [ ] #3 The run reaches a terminal status (done/failed) on its own; the dash, polling the artifact, reflects each transition with no dash-side mutation
- [ ] #4 stop_conditions on the run/template are read by the coordinator and gate the terminal brief
- [ ] #5 Spawn emits a signal the coordinator wakes on (start message or artifact-watch), documented in the contract
- [ ] #6 Decision recorded (ADR or this ticket): extend cmd/sextant-workflow vs new client; the old sextant.workflow/v1 path's fate noted
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Extend clients/go/apps/workflow to subscribe run artifacts / a run-start subject, walk steps (work/checkpoint/brief), CAS-update status+activity+artifacts; adapt the old engine's pause/approve/cancel verbs to the new step kinds.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: capability-gap audit 2026-06-24 (the 'does the run actually work?' investigation; live run 01KVYADZ4ET154VY7E5C4H54S2 frozen). Back half of [[task-216]] (frontend live-state shipped). Relates [[task-193]] (contract), [[task-108]] (ticket->PR workflow), [[task-98]] (LLM orchestrator). Subsumes dash dead-affordances: Spawn-work run + 'steer this run' run-topic post. Cross-link [[feat-run-checkpoint-resume]], [[feat-run-cancel-stop]], [[feat-consolidate-workflow-surfaces]]. Bright-line: orchestration/autonomy seam — needs human design sign-off.

Why this was never built (forensic review of prior sessions, 2026-06-24): the work-engine lane was scoped as a UX surface + a record-shape CONTRACT, never as a running system. (1) TASK-193 ('the data step') has 5 ACs — all about defining sextant.workflow.run/v1 + template/v1, the relates:toward link, the stop-prompt array, and listability; NOT ONE requires a process that advances a run's status/steps. (2) ADR-0048 explicitly keeps execution out of the substrate and names 'the coordinator' as if it already exists. (3) The 18-slice EPIC C breakdown had no executor slice — its six children are all dash surfaces + the contract. TASK-216 carried the execution-flavored ACs but framed step-walking as a FRONTEND timer ('walks its steps automatically on a timer'), and that sim was never built either (no setInterval advances steps; only SEED records hard-coded to status:done). (4) The design assumed the pre-existing sextant-workflow coordinator covered execution — but the redesign simultaneously SUPERSEDED that coordinator's contract (sextant.workflow/v1 -> sextant.workflow.run/v1) without bridging it. The gap was known at ship time: workengine.jsx:99-100 literally comments 'the coordinator that writes real runs (TASK-193) doesn't exist'. No transcript shows the missing executor being raised and consciously deferred — it fell through the 'it's just an extension, the coordinator already exists' framing. Lesson for this ticket: the executor is genuinely new work against a contract that replaced the old engine's, not a tweak to the existing coordinator.

Execution-model decisions (Lena, 2026-06-26): the executor is PROGRAMMATIC + TRUSTING, but enforces (1) no-done-without-output — a work/brief step completes only on a done-signal carrying >=1 artifact or a non-empty summary; empty done is ignored (generalizes the brief-artifact gate to every step); and (2) rest = enforcement point — the coordinator watches the worker's agent.activity turn_end (the TASK-235 feed) and, on a rest with no valid output, REVIVES the worker (ADR-0045 drain-and-revive) with a 'post your output' nudge, bounded -> blocked. This needs TASK-235's turn_end signal (no other 'worker at rest' bus signal exists today) — hence dep on TASK-235 and TASK-235 ships first. Quality JUDGMENT (review/redo/edit) is NOT in this base executor — that is the opt-in agent mode, split out to [[feat-agent-mode-run-coordinator]] (TASK-242); this ticket stays the programmatic base.
<!-- SECTION:NOTES:END -->
