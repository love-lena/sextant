---
id: TASK-118
title: >-
  Workflow worker Bash least-privilege: scope to worktree, deny destructive +
  GUI commands
status: To Do
assignee: []
created_date: '2026-06-15 17:13'
updated_date: '2026-06-29 23:00'
labels:
  - P1
dependencies: []
priority: high
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
- [ ] #1 A single operator-facing flag (e.g. SX_PI_SANDBOX_MODE; DEFAULT = sandbox) switches a dispatched worker between two modes, easily toggled by the operator: 'sandbox' (hard OS jail, no reviewer-escape) and 'automode' (regular pi-auto — reviewer-adjudicated, escapable). Proof: flipping the flag demonstrably changes enforcement — a probe DENIED under sandbox is reviewer-adjudicated under automode. Flipper: mechanical + operator. Fake-pass guard: a flag that does not actually change worker behavior fails.
- [ ] #2 SANDBOX mode = HARD WALL: the worker runs under the OS sandbox (@foxfirecodes/sandbox-runtime / srt) with a scoped profile, NO pi-auto/reviewer in the enforcement path. An INSTRUCTED out-of-scope write, a read of a protected path, external network egress, AND destructive/GUI/system commands (osascript/killall/open -a/installs/force-push/shutdown) are ALL DENIED at the OS layer with NO escape. Proof: a worker TOLD to do each is denied, 3x repro per class. Flipper: mechanical + operator. Fake-pass guard: 'instructed -> allowed' must NOT happen — there is no reviewer to escape; this is exactly the case verbatim pi-auto FAILED.
- [ ] #3 AUTOMODE = regular pi-auto: loads pi-auto with the operator's settings VERBATIM (escape-only + codex-verbatim reviewer), behaving as the operator's interactive pi. Proof: pi-auto active + the reviewer adjudicates a probe (the escapable behavior). Flipper: mechanical. This is the documented escapable mode the operator opts into; its softer guarantee is intended.
- [ ] #4 BOTH modes: the worker does real in-scope work unimpeded (edit/build/test AND its bus tools — sextant_reply / artifact create), runs in a scoped per-run CWD, and FAILS LOUD (refuses to spawn) if the scope is missing or the sandbox runtime is unavailable — NEVER silently unconfined, NEVER bricked. Proof: a sandbox-mode worker completes an in-scope edit+build+test and replies on the bus; a missing scope / unavailable srt refuses to spawn. Flipper: mechanical. Fake-pass guard: a sandbox-mode worker that can't use its bus tools (bricked) fails; a silently-unconfined worker fails.
- [ ] #5 ZERO macOS TCC popups in the DEFAULT (sandbox) mode on the LIVE launchd-managed dispatcher: base template + a real multi-step workflow end-to-end raise no 'sextant wants to access ...' prompts. Proof: live run, operator observes no prompts. Flipper: operator. Fake-pass guard: verified on the launchd-MANAGED dispatcher (where TCC applies), NOT a dev-shell run where grants already exist.
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: the 2026-06-15 Firefox-close scare (workers cleared by transcript
grep, but orion + canopus flagged the unbounded blast radius). Sibling of
[[feat-wf-release-pr]] (release-path guard) — both shell-enforce the harness's
autonomy boundaries and are landing in one harness PR. Ref:
[[project_agentic_dev_workflow.md]].

Escalated P2->P1 (2026-06-29): now that pi is the work engine's SOLE/default harness (ADR-0052), EVERY dispatched worker is an unscoped coding agent — the dispatcher plist sets no WorkingDirectory and pi.sh does not cd, so pi runs under launchd with full file/Bash tools and roams the filesystem. Live symptom: the operator gets recurring macOS TCC popups ('sextant wants to access <Desktop/Documents/...>') from worker fs access — new since pi-as-harness. Same root as the Firefox-close scare, now on the default path. Mitigation applied: stopped the managed dispatcher to halt spawns. Real fix: scope the pi worker (constrained CWD/worktree + restricted tools; a pure 'write a poem' worker needs no fs at all). Cross-link [[feat-agent-mode-run-coordinator]] (TASK-242), [[bug-spawn-form-drops-chosen-template]] (TASK-248).

RE-SCOPED 2026-06-29 (operator directive): a FLAG switches dispatched workers between 'sandbox' (hard OS jail via srt / @foxfirecodes/sandbox-runtime, no pi-auto reviewer, no escape) and 'automode' (pi-auto verbatim, reviewer-adjudicated, escapable). Default = sandbox for unattended workers. Background: verbatim pi-auto escape-only proven NOT to confine an instructed/injected worker (reviewer approves escapes; the bus-worker threat model has no interactive user to authorize). sandbox mode = Mechanism 2 (outer srt wrap); the real work is the srt profile (egress allowlist: model API + NATS bus socket + session-dir writes; deny GUI/process-control + everything else). Supersedes the pi-auto-only re-scope (#298, closed) and the custom gate.ts (#296, closed).
<!-- SECTION:NOTES:END -->
