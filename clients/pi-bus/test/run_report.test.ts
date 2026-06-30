// Unit tests for the workflow-run step-done reporter (ADR-0048 + the D7 fix). They
// pin the invariant the D7 defect violated: a run step whose worker genuinely
// produced output (a bus artifact it created, OR file changes in its worktree)
// reports those artifacts on step-done, so the coordinator's proof gate passes; a
// truly hollow step still reports zero and is still blocked.
//
// The D7 root cause, reproduced RED→GREEN here: the step-done used to be emitted on
// the FIRST agent_end, capturing `produced` at that instant. agent_end fires once
// per agent run and can fire MID-TASK (a retryable stop / a follow-up turn), so the
// emit went out with artifacts:0 BEFORE the artifact-creating turns ran. The fix
// reads the produced set LAZILY, at the worker's true terminal point (the drain).
// The RED case below is the eager capture (empty at the early point); the GREEN
// case is the reporter reading the snapshot at report time (complete).

import { test } from "node:test";
import assert from "node:assert/strict";
import { RunReporter, type ProducedArtifact } from "../src/run_report.js";
import type { JSONValue } from "@sextant/sdk";

// A recording publish that captures every run.event the reporter emits.
function recordingPublish(): { publish: (s: string, r: JSONValue) => Promise<void>; events: { subject: string; record: Record<string, unknown> }[] } {
  const events: { subject: string; record: Record<string, unknown> }[] = [];
  return {
    events,
    publish: async (subject, record) => {
      events.push({ subject, record: record as Record<string, unknown> });
    },
  };
}

// artifactsOf reads the artifacts array off the one emitted run.event.
function artifactsOf(record: Record<string, unknown>): ProducedArtifact[] {
  return (record["artifacts"] as ProducedArtifact[]) ?? [];
}

test("RED→GREEN: the step-done carries artifacts produced AFTER an early agent_end would have fired", async () => {
  // The worker's produced set grows over the run. The race: an early agent_end fires
  // while produced is still EMPTY, then the artifact-creating turns run.
  const produced: ProducedArtifact[] = [];
  const { publish, events } = recordingPublish();
  const reporter = new RunReporter({
    runEventsSubject: "msg.topic.run.r306.events",
    runStep: "s3",
    selfId: () => "worker-ulid",
    publish,
    producedSnapshot: () => [...produced], // lazy: read AT report time
    log: () => {},
  });

  // RED reproduction: the OLD path captured produced eagerly at the first agent_end.
  // Simulate that instant — produced is still empty.
  const eagerCaptureAtFirstAgentEnd = [...produced];
  assert.equal(eagerCaptureAtFirstAgentEnd.length, 0, "RED: at the first agent_end the produced set is still empty (the D7 emit reported 0)");

  // The worker then DOES the work: creates the artifacts (the turns that ran after
  // the early agent_end).
  produced.push({ name: "build.r306", kind: "build", version: 4609 });
  produced.push({ name: "review.r306", kind: "review", version: 4610 });

  // GREEN: the reporter emits at the TRUE terminal point (the drain), reading the
  // snapshot NOW — so it carries the complete set, not the empty eager capture.
  await reporter.report("auto_drain_idle");

  assert.equal(events.length, 1, "exactly one step-done emitted");
  const arts = artifactsOf(events[0].record);
  assert.equal(arts.length, 2, "GREEN: the step-done carries BOTH artifacts the worker produced");
  assert.deepEqual(
    arts.map((a) => a.name).sort(),
    ["build.r306", "review.r306"],
    "the reported refs are the real artifacts (proof gate passes)",
  );
  assert.equal(events[0].record["status"], "done");
  assert.equal(events[0].record["step"], "s3");
  assert.equal(events[0].record["by"], "worker-ulid");
});

test("a CODE step with no bus artifact reports its captured worktree diff (passes the gate)", async () => {
  const { publish, events } = recordingPublish();
  const reporter = new RunReporter({
    runEventsSubject: "msg.topic.run.r307.events",
    runStep: "s2",
    selfId: () => "coder-ulid",
    publish,
    producedSnapshot: () => [], // the model created NO bus artifact
    captureDiff: async () => ({ name: "work.diff.s2", kind: "work.diff", version: 1 }),
    log: () => {},
  });

  await reporter.report("auto_drain_idle");

  const arts = artifactsOf(events[0].record);
  assert.equal(arts.length, 1, "the captured diff is reported as the deliverable");
  assert.equal(arts[0].name, "work.diff.s2");
  assert.equal(arts[0].kind, "work.diff");
});

test("a code step's diff does NOT duplicate a model artifact of the same name", async () => {
  const { publish, events } = recordingPublish();
  const reporter = new RunReporter({
    runEventsSubject: "x",
    runStep: "s1",
    selfId: () => "w",
    publish,
    producedSnapshot: () => [{ name: "work.diff.s1", kind: "work.diff", version: 2 }],
    captureDiff: async () => ({ name: "work.diff.s1", kind: "work.diff", version: 9 }),
    log: () => {},
  });
  await reporter.report("drain");
  const arts = artifactsOf(events[0].record);
  assert.equal(arts.length, 1, "no duplicate when the diff name already in produced");
  assert.equal(arts[0].version, 2, "the already-produced ref wins (no clobber)");
});

test("a TRULY hollow step (no artifact, no changes) still reports zero — the gate must still block", async () => {
  const { publish, events } = recordingPublish();
  const reporter = new RunReporter({
    runEventsSubject: "x",
    runStep: "s1",
    selfId: () => "w",
    publish,
    producedSnapshot: () => [],
    captureDiff: async () => undefined, // not a git repo / no changes — no-op
    log: () => {},
  });
  await reporter.report("auto_drain_idle");
  const arts = artifactsOf(events[0].record);
  assert.equal(arts.length, 0, "a hollow step reports zero artifacts (coordinator blocks it — gate NOT weakened)");
});

test("report is idempotent — emitted exactly once across drain + shutdown", async () => {
  const produced: ProducedArtifact[] = [{ name: "a", kind: "k", version: 1 }];
  const { publish, events } = recordingPublish();
  const reporter = new RunReporter({
    runEventsSubject: "x",
    runStep: "s1",
    selfId: () => "w",
    publish,
    producedSnapshot: () => [...produced],
    log: () => {},
  });
  await reporter.report("auto_drain_idle");
  await reporter.report("session_shutdown"); // the defensive second call
  await reporter.report("handoff_drain");
  assert.equal(events.length, 1, "exactly one step-done despite three report() calls");
});

test("a non-run-step worker (no runEventsSubject) never emits", async () => {
  const { publish, events } = recordingPublish();
  const reporter = new RunReporter({
    runEventsSubject: "", // plain mobilize / revive — not a run step
    runStep: "",
    selfId: () => "w",
    publish,
    producedSnapshot: () => [{ name: "a", kind: "k", version: 1 }],
    log: () => {},
  });
  assert.equal(reporter.isRunStep(), false);
  await reporter.report("auto_drain_idle");
  assert.equal(events.length, 0, "a non-run worker emits no run.event");
});

test("with no live client the report defers (does NOT latch) so a later call can still emit", async () => {
  let clientId = ""; // no live client yet
  const produced: ProducedArtifact[] = [{ name: "a", kind: "k", version: 1 }];
  const { publish, events } = recordingPublish();
  const reporter = new RunReporter({
    runEventsSubject: "x",
    runStep: "s1",
    selfId: () => clientId,
    publish,
    producedSnapshot: () => [...produced],
    log: () => {},
  });
  await reporter.report("auto_drain_idle"); // no client → deferred, not latched
  assert.equal(events.length, 0, "deferred while no client");
  assert.equal(reporter.hasReported(), false, "not latched — a later call may still emit");
  clientId = "w"; // client now live (e.g. at shutdown after a transient gap)
  await reporter.report("session_shutdown");
  assert.equal(events.length, 1, "the later call emits once the client is live");
});

test("captureDiff failure does not block the report (model artifacts still go out)", async () => {
  const { publish, events } = recordingPublish();
  const reporter = new RunReporter({
    runEventsSubject: "x",
    runStep: "s1",
    selfId: () => "w",
    publish,
    producedSnapshot: () => [{ name: "build.x", kind: "build", version: 3 }],
    captureDiff: async () => {
      throw new Error("git exploded");
    },
    log: () => {},
  });
  await reporter.report("auto_drain_idle");
  const arts = artifactsOf(events[0].record);
  assert.equal(arts.length, 1, "the model-created artifact still reports despite a diff-capture failure");
  assert.equal(arts[0].name, "build.x");
});

// --- D8: the worker can latch a BLOCKED outcome (layered onto the D7 reporter) ---
//
// The load-bearing D8 property: a verifier worker that finds the Definition of Done unmet
// must emit outcome:"blocked", because the coordinator's runVerify gate keys SOLELY on
// outcome=="blocked" to block the run. Before this the outcome was hard-coded "done", so
// the gate was dead code in production — only the coordinator's TEST fake could ever
// produce blocked. These tests prove the REAL reporter (the same drain-emit path D7 fixed)
// now produces a blocked run.event with status:"done" + outcome:"blocked" + reason — and
// that the diff-capture + complete-produced-set behaviour D7 added is UNCHANGED on this path.

// blockableReporter builds a reporter over the deps seam with a recording publish.
function blockableReporter(produced: ProducedArtifact[] = []): { reporter: RunReporter; events: { subject: string; record: Record<string, unknown> }[] } {
  const { publish, events } = recordingPublish();
  const reporter = new RunReporter({
    runEventsSubject: "msg.topic.run.rBLOCK.events",
    runStep: "verify",
    selfId: () => "verifier-ulid",
    publish,
    producedSnapshot: () => [...produced],
    log: () => {},
  });
  return { reporter, events };
}

test("D8 default (no block): the drain emit still carries outcome:done", async () => {
  const { reporter, events } = blockableReporter();
  assert.equal(reporter.isBlocked(), false);
  await reporter.report("auto_drain_idle");
  assert.equal(events[0].record["status"], "done");
  assert.equal(events[0].record["outcome"], "done", "unblocked path still reports done (D7 path unchanged)");
  assert.equal("reason" in events[0].record, false, "no reason on a clean outcome");
});

test("D8: markBlocked makes the drain emit status:done + outcome:blocked + reason (THE D8 property)", async () => {
  const { reporter, events } = blockableReporter();
  reporter.markBlocked("go build ./... failed: undefined Foo");
  await reporter.report("auto_drain_idle"); // the REAL drain path, not the fake dispatcher

  assert.equal(events.length, 1, "exactly one step-done");
  // status STILL "done" (the step finished) — the OUTCOME is blocked. The exact shape the
  // coordinator's onEvent (status=="done") + runVerify (outcome=="blocked") read; a real
  // broken-DoD verdict now reaches the gate that was previously dead code.
  assert.equal(events[0].record["status"], "done", "status:done — the step ran to completion");
  assert.equal(events[0].record["outcome"], "blocked", "outcome:blocked — the coordinator blocks the run on this");
  assert.equal(events[0].record["reason"], "go build ./... failed: undefined Foo", "the reason rides a typed field for the activity trail");
  assert.equal(reporter.isBlocked(), true);
  assert.equal(reporter.reason(), "go build ./... failed: undefined Foo");
});

test("D8: a blocked verifier STILL reports its produced verdict + captured diff (D7 behaviour intact)", async () => {
  // A blocked verification does not suppress the deliverable proof channel — the verdict
  // artifact and any worktree diff still ride along (existence-gated by the coordinator).
  const { publish, events } = recordingPublish();
  const reporter = new RunReporter({
    runEventsSubject: "x",
    runStep: "verify",
    selfId: () => "w",
    publish,
    producedSnapshot: () => [{ name: "verdict.rBLOCK.verify", kind: "verdict", version: 1 }],
    captureDiff: async () => ({ name: "work.diff.verify", kind: "work.diff", version: 1 }),
    log: () => {},
  });
  reporter.markBlocked("AC#3 unmet");
  await reporter.report("auto_drain_idle");
  assert.equal(events[0].record["outcome"], "blocked");
  const arts = artifactsOf(events[0].record);
  assert.deepEqual(
    arts.map((a) => a.name).sort(),
    ["verdict.rBLOCK.verify", "work.diff.verify"],
    "verdict + captured diff both reported even when blocked (proof channel + diff-capture unchanged)",
  );
});

test("D8: markBlocked is a one-way latch, first reason wins", () => {
  const { reporter } = blockableReporter();
  reporter.markBlocked("first failure");
  reporter.markBlocked("second failure");
  // A later block (often fallout from the first) must not overwrite the earliest, most-
  // direct cause, and the latch can never flip back to done.
  assert.equal(reporter.reason(), "first failure", "first block wins");
  assert.equal(reporter.isBlocked(), true, "latch stays blocked");
});

test("D8: a non-run worker that latches blocked still emits nothing", async () => {
  const { publish, events } = recordingPublish();
  const reporter = new RunReporter({
    runEventsSubject: "", // not a run step
    runStep: "",
    selfId: () => "w",
    publish,
    producedSnapshot: () => [],
    log: () => {},
  });
  reporter.markBlocked("irrelevant — no run to block");
  await reporter.report("auto_drain_idle");
  assert.equal(events.length, 0, "no run.event off a run, even with a blocked latch");
});
