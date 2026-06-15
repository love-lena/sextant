---
id: TASK-95
title: >-
  Agentic dev workflow: an LLM orchestrator agent that drives
  plan→implement→codex-review→fix→brief→human-gate→release over the bus
status: In Progress
assignee: []
created_date: '2026-06-15 01:32'
updated_date: '2026-06-15 01:50'
labels:
  - feature
  - orchestration
  - workflow
  - 'slug:feat-agentic-dev-workflow'
  - P2
  - ready-for-agent
dependencies: []
priority: high
ordinal: 97000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
lena's flagship use of the orchestration: a single LLM orchestrator AGENT (not a bespoke Go engine) drives a full dev pipeline by spawning a fresh worker per step and resuming at each handoff. Pipeline: plan (superpowers writing-plans -> PLAN.md) -> implement -> [codex review <-> fix, sticky fixer, until approved] -> brief -> [codex review-brief <-> revise] -> human GATE (approve/changes) -> release. Architecture B: the orchestrator is a pure coordinator that spawns named workers on the bus (claude for plan/implement/fix/brief, codex for reviews) and reuses the SAME fixer across a revision loop (dampens infinite loops). Leverages the LLM to orchestrate instead of encoding a state machine in Go. Test target: the sextant repo itself, on the real bus. Design: artifact agentic-dev-workflow-design.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 An orchestrator playbook/skill encodes the pipeline, the spawn-a-worker protocol, the bounded review<->fix loop (sticky fixer), the human-gate protocol, and the guardrails
- [ ] #2 A fresh worker is spawned per step with a named bus identity (planner/implementer/codex-reviewer/fixer/brief); the fixer is reused (resumed) across revision rounds
- [ ] #3 codex reviewers report approved|changes-requested + notes; the orchestrator loops fix until approved, bounded (fail-loud) to avoid infinite loops
- [ ] #4 A human gate posts 'ready for review' to lena's DM + a progress artifact, then the orchestrator yields and resumes (M5.1 wake loop) on her approve / changes+feedback control
- [ ] #5 Guardrails: all work isolated to a git worktree+branch (never the operator's main checkout); release = gh pr create (open PR, NOT auto-merge); the release step is gated on lena's approve
- [x] #6 A token-free stub dry-run proves the harness plumbing (worktree setup, named-identity registration, the gate control path, the open-PR path) on a throwaway bus
- [ ] #7 The live run is operator-driven (lena): the safety classifier blocks an unattended session from launching autonomous editing agents; a one-command harness lets her drive it on the real bus
<!-- AC:END -->



## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
v1, Architecture B on the sextant repo: (1) orchestrator playbook prompt; (2) worker prompt templates (planner/implementer/codex-reviewer/fixer/brief); (3) run harness = sextant worktree+branch + register a top-level orchestrator identity + launch claude -p with the playbook under the spawn-poc wake supervisor (watching the control subject for the gate) + scoped tools (Bash/git/gh/Read/Edit + sextant MCP); (4) token-free stub dry-run for the plumbing; (5) notes. Composes M5.1 (wake/resume) + M5.2 (spawn). lena drives the live run.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: lena's orchestration-m5 session 2026-06-14, real-use-case request. Design (with the A-vs-B + blast-radius decisions): artifact agentic-dev-workflow-design. Composes [[feat-m5-dispatcher]] (spawn) + the M5.1 spawn spike (wake/resume). Decisions: B (coordinator spawns workers) over A (orchestrator does the work itself); sextant repo as the test target; real bus; release=open-PR not auto-merge for v1.
<!-- SECTION:NOTES:END -->
