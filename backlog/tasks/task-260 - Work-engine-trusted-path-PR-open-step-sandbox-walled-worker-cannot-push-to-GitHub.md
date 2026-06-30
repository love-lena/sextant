---
id: TASK-260
title: >-
  Work-engine: trusted-path PR-open step (sandbox-walled worker cannot push to
  GitHub)
status: To Do
assignee: []
created_date: '2026-06-30 04:14'
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
Prerequisite: [[feat-per-run-isolated-worktree]] (TASK-256) — the branch to push must be the run's isolated worktree branch. Decision on option (a/b/c) needed first; file an operator-decision artifact before implementation. The trusted push entity needs git credentials with push access to the target repo — clarify whether this is the operator's ambient creds (keychain) or a scoped deploy key.
<!-- SECTION:NOTES:END -->
