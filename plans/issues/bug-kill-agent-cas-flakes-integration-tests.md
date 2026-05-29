---
title: kill_agent CAS bail-on-conflict trips integration tests with "definition changed during kill (concurrent restart/archive)"
status: fixed
priority: P2
created_at: 2026-05-27T20:47-07:00
fixed_in: 925fb19
labels: [bug, daemon, rpc, test, flake, cas, regression]
discovered_in: post-merge of kill_agent CAS migration (commit ceb0bb2) — multiple subagents running the full `make test-go` matrix on 2026-05-27 hit identical failures in `cmd/sextantd` integration tests; reproduces on `main` with no local changes
---

## Symptom

`make test-go` against `main` (commit a93d031 or later) fails the
following `cmd/sextantd` integration tests with the same error
message:

- `TestAgentCanEditWorkspaceFile` (spawn → prompt → kill flow)
- `TestM11SpawnFlowAcceptance`
- `TestM12CLIBinaryWalkthroughAcceptance`
- `TestSidecarSDKDriverMockRoundTrip`
- `TestSidecarSDKDriverMockErrorPath`

Representative log line (from `TestAgentCanEditWorkspaceFile`):

```
kill_agent: rpc bad_request: agent <uuid> definition changed during kill
(concurrent restart/archive); the container was stopped — re-issue kill
against the new incarnation if appropriate
```

The container WAS stopped — kill_agent's side effect ran — but the
final def write CAS-conflicted, so the handler returned
`BAD_REQUEST` and the test's assertion that kill returned cleanly
fails.

## Root cause (hypothesis)

The kill_agent CAS migration in commit `ceb0bb2` switched the
final def write from plain `Put` to `Update(revision)` (good — it
fixed real production races). The BAIL-on-conflict semantics
(deliberately chosen, mirroring `restart_agent`'s rollback shape)
mean any concurrent def mutation between the initial Get and the
final Update returns `BAD_REQUEST`.

The integration tests likely race against the **daemon's own
lifecycle convergence work** — the L2 reconciler (PR #2,
`add5db0`) periodically reads-modifies-writes def state to align
KV with truth. If the reconciler fires between kill's Get and
Update, kill bails. The reconciler's write is itself legitimate
("agent now stopped, mark def lifecycle=stopped"), not a true
concurrent operator action.

Confirmed by subagent reports during the 2026-05-27 dispatch
session: every agent that ran the full matrix saw the same
failure on `main` with no local diff applied. The CAS test
`TestRestartAgentRespectsConcurrentArchive` (using `incs.putHook`
to inject the race) passes in isolation — the production code is
correct under genuine concurrent mutations. The flake is the
test environment + reconciler interaction, not the CAS itself.

## Why P2

This blocks the basic integration test signal. Every subagent
dispatched tonight noted "the only failing tests are these N
in cmd/sextantd, pre-existing on main." That's a quiet but real
form of test rot: developers (human and agent) learn to ignore
failing tests, and a future real regression gets lost in the
noise. Worth fixing while the cause is fresh.

Promoting from P3 because **multiple agents had to call this out
to disambiguate from their own work**. The cost in CI / review
trust outweighs the cost of the fix.

## Fix shape (candidates)

### 1. Quiesce the reconciler while kill runs (preferred)

Take a per-agent lock that kill / restart / archive hold, and
that the reconciler waits on. The reconciler already needs to be
race-aware (its own CAS budget) — having operator handlers hold a
"this agent is being mutated" advisory lock would let the
reconciler back off cleanly.

Cost: lock primitive in the daemon (probably a per-agent mutex
keyed by UUID); reconciler check before its CAS attempt.

### 2. Retry budget on kill_agent CAS

Mirror `lifecycle_watcher`'s 3-retry budget. If kill's CAS bails,
re-Get the def, re-build the mutated form, re-attempt. Cap at
N retries; on exhaustion, return BAD_REQUEST as today.

Cost: small change to `pkg/rpc/handlers/kill.go`. Test pattern
exists in `lifecycle_watcher.go`.

Tradeoff: kill_agent has side effects (container stop). The
existing BAIL shape exists *because* of those side effects — on
the retry, the container is already stopped but the def-state
might be a fresh incarnation that we shouldn't stop. Retrying
the def-write only (not the side effect) is safe; restart_agent
made the opposite call (bail to avoid orphaned containers). The
asymmetry is fine if documented.

### 3. Skip the def-state convergence in the reconciler

The reconciler's writes are best-effort convergence. If a
concurrent operator handler is touching the agent, the
reconciler could skip and try again on the next tick instead of
fighting for the revision.

Cost: reconciler logic to detect "recent operator-handler touch"
(maybe a timestamp on the def with a short ignore window).

### 4. Test-only: serialize the reconciler in test setup

In `cmd/sextantd`'s test harness, run with the reconciler interval
set very high (or disabled). Cheapest fix; doesn't help
production races but unblocks the test signal.

Cost: harness flag wiring. Doesn't address the underlying race.

## Recommendation

Combination of **(2) retry budget on kill_agent** as the production
fix + **(4) reconciler-quiet test harness** as the immediate CI
unblock. (1) is the long-term "principled" answer but the
locking surface needs design. Track (1) as a follow-up if (2)+(4)
proves insufficient.

## Acceptance

- `make test-go` against this branch passes the 5 listed tests
  reliably (run 10x consecutively to verify the flake is gone).
- Existing `TestRestartAgentRespectsConcurrentArchive` /
  `TestRestartAgentRespectsConcurrentKill` /
  `TestArchiveAgentRespectsConcurrentRestart` still pass — the
  CAS-conflict semantics are unchanged for genuine concurrent
  operator actions.
- New test: `TestKillAgentRetriesOnReconcilerRace` exercises the
  retry budget under a hooked reconciler-shaped def write.

## Related

- `pkg/rpc/handlers/kill.go` — the handler.
- `pkg/rpc/handlers/restart.go` — the asymmetric BAIL shape.
- `pkg/rpc/handlers/archive.go` — migrated to CAS tonight
  (`feat/handlers-cas-archive-agent`), same shape.
- `pkg/sextantd/lifecycle_watcher.go` — the reconciler.
- Commit `ceb0bb2` — the CAS migration that introduced the flake.
- `[[feat-handlers-cas-writes]]` — parent CAS sweep.
- `[[bug-flake-daemon-restarts-nats-after-kill]]` — neighbor in the
  integration-test flake family.
