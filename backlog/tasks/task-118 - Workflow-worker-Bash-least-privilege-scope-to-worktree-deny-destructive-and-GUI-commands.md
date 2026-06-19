---
id: TASK-118
title: 'Workflow worker Bash least-privilege: scope to worktree, deny destructive + GUI commands'
status: To Do
assignee: []
created_date: '2026-06-15 17:13'
labels:
  - feature
  - orchestration
  - workflow
  - security
  - 'slug:feat-worker-bash-least-privilege'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 118000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The agentic-dev-workflow spawns workers (claude/codex) with broad `Bash` +
`acceptEdits`. A worker that over-eagerly "verifies" its change can reach the
operator's GUI and system: open/close apps, `osascript`, `killall`, install
software — well outside its job of editing files in a worktree. Surfaced during
a scare where the operator's Firefox windows closed twice; a transcript grep
cleared the workers that time, but the blast radius is real and unbounded.

Fix shape: scope the worker's `Bash` to the task. Confine writes/cwd to the
worktree, and deny the destructive/GUI/system command classes (`killall`,
`pkill`, `osascript`, `open -a`, package installs, force-push, etc.) at the
shell level — the same shell-guard pattern as the `wf-release-pr` wrapper
([[feat-wf-release-pr]]). Least-privilege by default; the worker can still do
its real work (read, edit, build, test in-tree).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A worker's Bash cannot run GUI/system commands (killall/pkill/osascript/open -a/installs) — blocked with a clear error
- [ ] #2 Worker file writes + cwd are confined to its worktree; escapes are denied
- [ ] #3 The worker can still read, edit in-tree, build, and run tests (real work is unimpeded)
- [ ] #4 The guard is enforced at the shell level (not playbook compliance), mirroring the wf-release-pr wrapper
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: the 2026-06-15 Firefox-close scare (workers cleared by transcript
grep, but orion + canopus flagged the unbounded blast radius). Sibling of
[[feat-wf-release-pr]] (release-path guard) — both shell-enforce the harness's
autonomy boundaries and are landing in one harness PR. Ref:
[[project_agentic_dev_workflow.md]].
<!-- SECTION:NOTES:END -->
