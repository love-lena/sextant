---
id: TASK-118
title: >-
  Workflow worker Bash least-privilege: scope to worktree, deny destructive +
  GUI commands
status: To Do
assignee: []
created_date: '2026-06-15 17:13'
updated_date: '2026-06-29 21:27'
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
- [ ] #1 Dispatched pi workers LOAD pi-auto (yonilerner/pi-auto) using the OPERATOR'S settings VERBATIM (~/.pi/agent/extensions/pi-auto.json: sandbox.mode=escape-only, allowWrite=["."], allowedDomains=[], useCodexAutoReview=true, reviewerPolicySource=codex-verbatim) — not a reimplementation, not a forked/weakened config. Proof: a spawned worker's pi session shows pi-auto active with those exact settings. Flipper: mechanical + operator. Fake-pass guard: a worker that loads a weakened config OR does not load pi-auto at all (the current -ne no-discovery path) fails.
- [ ] #2 The worker's BASH runs under pi-auto's OS sandbox (@foxfirecodes/sandbox-runtime via sandbox-exec): a bash write OUTSIDE the scoped CWD is denied, and outbound NETWORK egress is denied (allowedDomains=[]). Proof: a live/test worker whose bash attempts an out-of-CWD write or an external curl is sandbox-killed/denied. Flipper: mechanical + operator. Fake-pass guard: a worker TOLD to write-outside / exfiltrate via bash still cannot — OS-enforced, not reviewer-waved.
- [ ] #3 The worker runs in a scoped per-run CWD (a worktree/scratch under the store) set BEFORE pi launches, so allowWrite=["."] confines writes to that scope; and the worker FAILS LOUD (refuses to spawn) if the scope is missing OR pi-auto's sandbox is configured-but-unavailable (sandbox.ts checkSandboxAvailability) — NEVER a silently-unsandboxed worker. Proof: inspect a spawned worker's CWD == scope; a missing scope or unavailable sandbox refuses to spawn. Flipper: mechanical.
- [ ] #4 Destructive/GUI/system actions (osascript, killall, pkill, open -a, package installs, git force-push, shutdown) are DENIED by pi-auto's reviewer policy (codex-verbatim: high/critical risk -> deny), and real in-tree work is unimpeded (known-safe commands fast-pathed; in-scope edit/build/test proceed). Proof: a worker that attempts osascript/killall is denied; a worker doing a normal edit+build+test in its scope completes. Flipper: operator + test. NOTE/Fake-pass guard: this is pi-auto's probabilistic reviewer layer (operator-accepted per pi-auto's design + the operator's codex-verbatim setting), NOT a hard OS deny — the AC is met when pi-auto denies these on the operator's settings; do not claim cryptographic enforcement for this class.
- [ ] #5 ZERO macOS TCC popups on a normal run on the LIVE launchd-managed dispatcher (base template AND a real multi-step workflow end-to-end). Proof: live run, operator observes no 'sextant wants to access ...' prompts. Flipper: operator. Fake-pass guard: verified on the launchd-MANAGED dispatcher (where TCC applies), NOT a dev-shell run where grants already exist.
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: the 2026-06-15 Firefox-close scare (workers cleared by transcript
grep, but orion + canopus flagged the unbounded blast radius). Sibling of
[[feat-wf-release-pr]] (release-path guard) — both shell-enforce the harness's
autonomy boundaries and are landing in one harness PR. Ref:
[[project_agentic_dev_workflow.md]].

Escalated P2->P1 (2026-06-29): now that pi is the work engine's SOLE/default harness (ADR-0052), EVERY dispatched worker is an unscoped coding agent — the dispatcher plist sets no WorkingDirectory and pi.sh does not cd, so pi runs under launchd with full file/Bash tools and roams the filesystem. Live symptom: the operator gets recurring macOS TCC popups ('sextant wants to access <Desktop/Documents/...>') from worker fs access — new since pi-as-harness. Same root as the Firefox-close scare, now on the default path. Mitigation applied: stopped the managed dispatcher to halt spawns. Real fix: scope the pi worker (constrained CWD/worktree + restricted tools; a pure 'write a poem' worker needs no fs at all). Cross-link [[feat-agent-mode-run-coordinator]] (TASK-242), [[bug-spawn-form-drops-chosen-template]] (TASK-248).

RE-SCOPED 2026-06-29 (operator directive): use pi-auto (yonilerner/pi-auto) EXACTLY with the operator's ~/.pi/agent/extensions/pi-auto.json settings, NOT a hand-rolled gate. Root cause: the pi recipe launches with -ne (extension discovery OFF) so pi-auto never loads -> workers run unsandboxed. Fix: load pi-auto + run in a scoped CWD. Enforcement split (honest): pi-auto OS-sandboxes BASH (writes->CWD, network denied) via @foxfirecodes/sandbox-runtime; in-process read/write/edit go through pi-auto's probabilistic reviewer (its README states it is NOT a security boundary). Operator accepts that posture. SUPERSEDES the custom gate.ts/PATH-guard approach in PR #296 (close it); KEEP only the scoped-CWD + fail-loud-if-unscoped recipe work.
<!-- SECTION:NOTES:END -->
