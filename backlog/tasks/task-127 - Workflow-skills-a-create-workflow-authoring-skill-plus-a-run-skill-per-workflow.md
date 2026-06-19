---
id: TASK-127
title: 'Workflow skills: a create-workflow authoring skill + a run-skill per workflow'
status: To Do
assignee: []
labels:
  - feature
  - workflow
  - skills
  - dx
  - 'slug:feat-workflow-skills'
  - P2
  - ready-for-human
dependencies: []
priority: medium
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena (2026-06-16): generalize the `/spike` pattern (TASK-125/#151) into a proper
skills surface for the workflow system, so workflows are created + run from
slash commands, never shell.

Two kinds of skill:
1. **A workflow-AUTHORING skill** (e.g. `/create-workflow`) — walks the operator
   (or an agent) through building a NEW workflow: name it, define the steps
   (which worker model per step — claude / gpt-5.5 / codex), what artifacts it
   reads/writes, gate or no gate — and scaffolds the harness + orchestrator
   playbook + a run-skill from the agentic-dev-workflow / research-spike template.
   The "meta" skill that turns 'I want a workflow that does X' into a runnable
   workflow without hand-writing the bash harness.
2. **A run-skill PER workflow** (e.g. `/spike`, and one per future workflow) —
   the one-command operator trigger that runs that workflow with its args and
   surfaces its output artifacts. `/spike` (#151) is the first; each new workflow
   the authoring skill creates should emit its own run-skill.

Honors [[feedback_no_shell_for_demos]] (operator triggers are slash commands,
the agent runs the harness underneath) and the operator-driven live-run
constraint (the classifier blocks unattended worker-spawning).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A `/create-workflow` (authoring) skill scaffolds a new workflow harness + orchestrator playbook + a dedicated run-skill from the template, parameterized by steps + per-step worker model
- [ ] #2 Each workflow has its own run-skill (`/spike` is the first; new workflows get one auto-generated) so it runs from one slash command, no shell
- [ ] #3 Generated workflows reuse the proven harness (wf-spawn claude/codex, stub+live modes, least-privilege workers); stub demo passes for a scaffolded example
- [ ] #4 Design gated through sirius before build (skill structure + the authoring flow are a real design surface)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Builds on [[feat-leaf-node-topology]]'s sibling work — the research-spike harness
(docs/demos/research-spike-workflow.sh) + `/spike` skill (.claude/skills/spike/)
from #151 are the template. The agentic-dev-workflow harness is the richer
template (with a gate). Discovered: Lena wants workflows to be a first-class,
skill-driven surface, not bespoke bash. Pairs with the write-a-skill engineering
skill for the scaffolding mechanics.
<!-- SECTION:NOTES:END -->
