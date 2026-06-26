---
id: TASK-241
title: >-
  Retire the demo apparatus: docs/demos scripts + the /spike and pi-live-demo
  skills
status: Done
assignee: []
created_date: '2026-06-26 21:12'
labels:
  - chore
dependencies: []
ordinal: 228000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The docs/demos/ walkthrough scripts were a mix of throwaway plumbing demos and — worse — hand-rolled bash orchestration that bypassed the real engine. research-spike-workflow.sh (run by the /spike skill) and violet-runtime.sh both ran an LLM orchestrator under a while-loop "supervisor" that re-invoked claude -p --resume, with shell wf-spawn/wf-progress helpers reimplementing the workflow coordinator + dispatcher. That is the exact deprecated pattern ADR-0045 reframed (a mobilized agent is a resumable one-shot function; the dispatcher revives dormant agents in-process). Keeping these scripts around taught agents the wrong pattern as if it were canonical. Decision (Lena, 2026-06-26): retire the whole demo apparatus — all of docs/demos/, the /spike skill, and the pi-live-demo plugin skill — rather than rebuild any of it. Supersedes TASK-240 (rebuild the retired M5 demos), which is no longer wanted.
<!-- SECTION:DESCRIPTION:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Deleted docs/demos/ in full; deleted the /spike skill (.claude/skills/spike) and the pi-live-demo plugin skill (clients/claude-code/skills/pi-live-demo). Dropped the dispatcher's vestigial --on-wake/--supervisor/--wake-timeout flags and their dead struct fields (deprecated+ignored since ADR-0045) and rewrote its package doc to the in-process-revive model. Fixed dangling doc-comment references in clients/{coordinator,sextant-cli/workflow,assistant/internal/violet}; removed the vestigial docs/demos/*.mp4 .gitignore line; bumped the plugin version (skill removal needs claude plugin update). make lint + make test (race) green. Supersedes TASK-240.
<!-- SECTION:FINAL_SUMMARY:END -->
