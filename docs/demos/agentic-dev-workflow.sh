#!/usr/bin/env bash
# Agentic dev workflow demo (TASK-98) — RETIRED (TASK-239).
#
# This demo's plumbing drove the human-GATE round-trip with the M5.1 spike
# supervisor (clients/go/apps/, now removed by the ADR-0049 restructure): the LLM
# orchestrator yielded at a gate and the supervisor re-invoked it on a control
# message (`--on-wake`). That wake mechanism was reframed by ADR-0045 (a mobilized
# agent is a resumable one-shot function — `pi --rpc` workers), so the supervisor
# binary this script built no longer exists and the demo can no longer run as
# written — in either its token-free `demo` mode or its live `run` mode.
#
# The orchestrator concept is unchanged and lives on:
#   - Design + walkthrough (historical): docs/demos/agentic-dev-workflow-notes.md and
#     docs/demos/agentic-dev-workflow-orchestrator.md.
#   - The live workflow path: the coordinator (clients/coordinator) drives a
#     declarative workflow whose steps dispatch agents via clients/dispatcher; the
#     browser dash creates/renders runs over @sextant/conv-workflow. The token-free
#     coordinator+dispatcher composition runs in docs/demos/m5-workflow-demo.sh.
#   - The resumable-worker model that replaces the wake-loop: docs/adr/0045-*.md.
#
# Rebuilding this flagship demo on the resumable-worker mechanism is a worthwhile
# follow-up (see TASK backlog); it is retired here rather than left building a
# deleted path.
echo "docs/demos/agentic-dev-workflow.sh is RETIRED (TASK-239): its human-gate wake"
echo "depended on the M5.1 supervisor, reframed by ADR-0045. See the orchestrator notes,"
echo "docs/demos/m5-workflow-demo.sh, and clients/coordinator + clients/dispatcher."
exit 0
