---
id: TASK-260
title: >-
  Work-engine: trusted-path PR-open step (sandbox-walled worker cannot push to
  GitHub)
status: To Do
assignee: []
created_date: '2026-06-30 04:14'
updated_date: '2026-06-30 05:56'
labels:
  - workengine
  - sandbox
  - github
  - trusted-path
  - P1
  - needs-triage
  - 'slug:feat-trusted-path-pr-open'
dependencies:
  - TASK-256
priority: high
ordinal: 246000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The work-engine must turn a completed code change into a real GitHub PR — but the sandbox mode confines egress to api.anthropic.com + loopback (github.com denied per TASK-118), so the jailed worker cannot push to GitHub or open a PR. The live-validation scaffold hand-built a coordinator that ran outside sandbox mode for the PR step. The managed path has no mechanism for this.

Options to evaluate and pick one: (a) a host-side trusted PR-open step kind — the managed coordinator itself (not the jailed pi worker) pushes the branch and opens the PR using the operator's git/gh credentials after verifying the worker's committed branch; (b) widen the dev-workflow sandbox egress to include github.com as an explicit allowlist entry; (c) run the PR step in a separate auto-mode (non-sandboxed) worker. Design must state the trust posture explicitly (what the trusted entity can do, on whose authority). Cross-link: [[task-98]] AC#5 and AC#10, [[task-254]] (sandbox egress).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A completed work-engine run opens a real GitHub PR whose diff implements the ticket — without the operator manually running git push or gh pr create. Proof: a live PR URL produced by the managed engine, whose diff is authored by the worker's commits. Flipper: operator (live). Fake-pass guard: a brief or artifact that CLAIMS a PR was opened, or a run.done status with no real PR link, FAILS this AC — the proof is the real PR URL + diff, not the engine's self-report.
- [ ] #2 The trust posture is explicit and documented: the trusted entity (coordinator or host-side step) has push access scoped to the run's worktree branch only (not a force-push to main), and the operator's git credentials are not passed into the sandboxed worker's environment. Proof: a trust-posture document (ADR or Implementation Notes) stating what the trusted entity can and cannot do; a test confirming the jailed worker's env has no GH_TOKEN or SSH key. Flipper: operator (design review). Fake-pass guard: "run the whole workflow in auto-mode (no sandbox)" is not an acceptable posture — the worker's code execution must remain sandboxed; only the PR-push step may run outside the sandbox.
- [ ] #3 The PR-open step is part of the standard dev-workflow template used by TASK-98, not a manual step the operator runs after the engine finishes. Proof: the workflow template definition includes a pr-open step kind, and a managed run drives it to a real PR without operator shell intervention. Flipper: operator (live template run). Fake-pass guard: "the operator runs gh pr create after the run" is not an engine-driven PR step.
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
DECISION: Option (a) — a host-side trusted PR-open STEP KIND (KindPROpen, "pr-open"). Implemented on rc/work-engine (feat/task-260-pr-open).

TRUST POSTURE (AC#2). The work engine's coding worker is a sandboxed pi process: its egress is walled to the model API only (github.com DENIED, TASK-118/ADR-0052) and it is never handed git/gh credentials (the dispatcher now SCRUBS GH_TOKEN/GITHUB_TOKEN/SSH_AUTH_SOCK/GIT_* from the worker-launch env — belt-and-suspenders alongside the egress wall). So the jailed worker physically cannot push a branch or open a PR; by design it only edits files in the run's isolated worktree (TASK-256) and leaves the diff uncommitted.

Turning that diff into a PR needs a TRUSTED host-side entity. The COORDINATOR is exactly that: a managed Go service on the operator's host (launchd) running with the operator's ambient git/gh authority, NOT inside the sandbox. A pr-open step runs in-process in the coordinator (NOT a spawn.request to the dispatcher): against the run's worktree (Run.Repo + branch sxrun/<runID>) it (a) refuses an empty diff (no empty PR — fail loud), (b) commits the pending changes on sxrun/<runID>, (c) pushes THAT branch to origin (explicit refspec, NEVER --force, never a shared branch like main), (d) opens a PR via gh against the run's base, and (e) records the PR URL as a typed produced artifact (sextant.pr/v1) so the existence gate passes and the URL is surfaced on the activity trail.

Scope boundary (here): one run branch, no force, base is the run's RepoRef (or main). Credential boundary (dispatcher): the worker's env carries no push creds. The two halves are the posture.

Credentials used: the operator's ambient git/gh keychain auth, inherited by the coordinator process on the host (NOT a scoped deploy key in this build).

OFFLINE-PROVEN vs LIVE-DEFERRED:
- AC#1 mechanism (offline): coordinator-driven run commits+pushes sxrun/<id> with the worker's change to a LOCAL bare origin; PR artifact recorded; only the gh pr-create return URL stubbed. The REAL-PR-URL on real GitHub is the LIVE half — deferred to the assembled e2e (TASK-98), NOT claimed met here.
- AC#1 empty-diff guard (offline): pr-open over an empty worktree fails loud.
- AC#2 (offline): trust posture documented (this note + clients/coordinator/pr.go); a dispatcher test confirms the jailed worker's env has NO GH_TOKEN/GITHUB_TOKEN/SSH_AUTH_SOCK even when the dispatcher's own env carries them.
- AC#3 (offline): the standard "Plan → build → review → PR" template now declares a pr-open step (was a manual operator action); Go+TS round-trip tests + the coordinator routes KindPROpen → runPROpen. Live template run = the e2e.
<!-- SECTION:NOTES:END -->
