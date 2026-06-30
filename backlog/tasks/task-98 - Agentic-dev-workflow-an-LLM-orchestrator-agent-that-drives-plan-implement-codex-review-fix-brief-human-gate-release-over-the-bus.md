---
id: TASK-98
title: >-
  Agentic dev workflow: an LLM orchestrator agent that drives
  plan→implement→codex-review→fix→brief→human-gate→release over the bus
status: In Progress
assignee: []
created_date: '2026-06-15 01:32'
updated_date: '2026-06-29 23:43'
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
- [ ] #8 sextant workflow run <name> reads a sextant.workflow.def/v1 artifact (task/base/models) and launches the orchestrator under the operator's authority; no env vars
- [ ] #9 Operator update (after the next release): cut a sextant release so the brew CLI has the workflow verb (sextant update + brew upgrade); until then run via the branch build (go run ./cmd/sextant workflow run <name>)
- [ ] #10 END-TO-END CAPSTONE (operator, 2026-06-29): the work-engine, given a single TASK-xxx ticket id as input, autonomously produces (a) a real GitHub PR against the repo whose diff implements the ticket AND (b) a brief artifact summarizing the work — via the plan -> build -> review -> PR workflow, on the LIVE setup. Proof: start the work-engine on a real small TASK ticket; observe a PR opened (link) whose diff actually addresses the ticket + a brief artifact; the operator confirms both. Flipper: operator (live). Fake-pass guard: a run reaching done WITHOUT a real PR opened — or a PR that does not address the ticket, or a brief that merely CLAIMS a PR — FAILS; the proof is the real PR link + diff, NEVER the run's self-reported done (ties to TASK-243's existence gate). Implementation note: the worker opening a PR needs github.com egress — reconcile with TASK-118 (sandbox-mode egress allowlist is api.anthropic.com + loopback bus only; github.com is denied) — either run this workflow in automode, widen the dev-workflow sandbox egress to github.com, or open the PR from the worker's committed branch via a path that has egress.
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
v1, Architecture B on the sextant repo: (1) orchestrator playbook prompt; (2) worker prompt templates (planner/implementer/codex-reviewer/fixer/brief); (3) run harness = sextant worktree+branch + register a top-level orchestrator identity + launch claude -p with the playbook under the spawn-poc wake supervisor (watching the control subject for the gate) + scoped tools (Bash/git/gh/Read/Edit + sextant MCP); (4) token-free stub dry-run for the plumbing; (5) notes. Composes M5.1 (wake/resume) + M5.2 (spawn). lena drives the live run.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: lena's orchestration-m5 session 2026-06-14, real-use-case request. Design (with the A-vs-B + blast-radius decisions): artifact agentic-dev-workflow-design. Composes [[feat-m5-dispatcher]] (spawn) + the M5.1 spawn spike (wake/resume). Decisions: B (coordinator spawns workers) over A (orchestrator does the work itself); sextant repo as the test target; real bus; release=open-PR not auto-merge for v1.

Trigger redesigned per lena (2026-06-14): workflow-as-artifact + 'sextant workflow run <name>' (no env-var harness). cmd/sextant/workflow.go (commit 23c6b6e); progress artifact is <id>.run (distinct from the <id> def). First live target: def artifact 'task62'; run-instructions in artifact task62-run-instructions.

Near-duplicate of task-108 (same agentic-dev-workflow slug). Reconcile - don't grab both.

Reframe (Lena, 2026-06-26): TASK-98 is 'the first AGENT-MODE workflow' — it runs under the long-lived per-run coordinator agent defined by [[feat-agent-mode-run-coordinator]] (TASK-242), layered on the programmatic base executor [[feat-run-executor-workflow-run-v1]] (TASK-236). The LLM orchestration this ticket wants = agent mode applied to the plan->implement->review->fix->brief->gate dev workflow.

Operator 2026-06-29: this is the work-engine GRADUATION CAPSTONE — the end-to-end proof that the engine does its job (TASK-xxx -> PR + brief via plan->build->review->PR). Depends on the whole stack: TASK-236 executor + TASK-244 pipe/capture + TASK-243 proof-gate + TASK-245 model routing + TASK-242 agent-mode (plan/build/review verbs) + TASK-118 sandbox + a plan->build->review->PR template. KEY OPEN: PR-push needs github.com egress, which sandbox mode denies — design the egress/mode for this workflow. Belongs in the batched live-verify as the capstone criterion.

Remaining capstone gaps (2026-06-30, from live e2e validation on managed/released path): the following tickets capture the production gaps the hand-run scaffold hid. All must be resolved before AC#10 can pass on the MANAGED path: [[feat-per-run-isolated-worktree]] (TASK-256) — per-run worktree provisioning; [[feat-work-engine-managed-coordinator-config]] (TASK-257) — managed coordinator 90s timeout + no-manual-flags; [[feat-work-engine-concurrent-runs]] (TASK-258) — concurrent run isolation + adoption; [[feat-run-start-adoption-reliability]] (TASK-259) — durable run.start adoption; [[feat-trusted-path-pr-open]] (TASK-260) — trusted-path PR-open (sandbox walls github.com); [[fix-agent-mode-reviewer-decision-emission]] (TASK-261) — reviewer cannot emit run.decision, all agent-mode runs wedge; [[fix-work-engine-robustness-artifact-nick]] (TASK-262) — artifact create-recovery + nick length truncation; [[chore-ship-d7-d8-to-managed-released-path]] (TASK-263) — D7+D8 must ship in released binaries.
<!-- SECTION:NOTES:END -->
