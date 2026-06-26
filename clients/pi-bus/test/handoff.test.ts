// Unit tests for the managed close-and-resume handoff (TASK-178, AC#3). They pin
// the two things the single-owner contract rests on, with no pi and no bus:
//   1. RECOGNITION — only a pi.handoff {verb:"drain"} is a drain; the worker's own
//      relinquished/acquired announcements (same $type) are NOT, nor is junk.
//   2. ORDERING — the wind-down runs in the exact single-owner order: wait for the
//      current turn to finish, announce relinquished{session}, THEN drain+close the
//      bus client (the visible release), THEN exit. And it is idempotent — a second
//      drain while one is in flight is a no-op (no double close / double exit).

import { test } from "node:test";
import assert from "node:assert/strict";
import {
  Handoff,
  isHandoffDrain,
  HANDOFF_TYPE,
  VerbDrain,
  VerbRelinquished,
  VerbAcquired,
  type HandoffDeps,
  type HandoffRecord,
} from "../src/handoff.js";
import type { JSONValue } from "@sextant/sdk";

test("isHandoffDrain recognises ONLY a drain, not announcements or junk", () => {
  assert.equal(isHandoffDrain({ $type: HANDOFF_TYPE, verb: VerbDrain } as JSONValue), true);
  assert.equal(isHandoffDrain({ $type: HANDOFF_TYPE, verb: VerbDrain, reason: "hand it over" } as JSONValue), true);
  // The worker's own announcements share the $type but are not a command to drain.
  assert.equal(isHandoffDrain({ $type: HANDOFF_TYPE, verb: VerbRelinquished, session: "s1" } as JSONValue), false);
  assert.equal(isHandoffDrain({ $type: HANDOFF_TYPE, verb: VerbAcquired, session: "s1" } as JSONValue), false);
  // Not a handoff at all → falls through to a normal wake (never swallowed).
  assert.equal(isHandoffDrain({ $type: "chat.message", text: "drain" } as JSONValue), false);
  assert.equal(isHandoffDrain({ verb: VerbDrain } as JSONValue), false);
  assert.equal(isHandoffDrain("drain" as unknown as JSONValue), false);
  assert.equal(isHandoffDrain(null as unknown as JSONValue), false);
  assert.equal(isHandoffDrain([{ $type: HANDOFF_TYPE, verb: VerbDrain }] as unknown as JSONValue), false);
});

// A recording deps seam: it appends a step name for each side effect so the test
// can assert the exact ORDER, and captures the announced records.
function recordingDeps(overrides: Partial<HandoffDeps> = {}): {
  deps: HandoffDeps;
  steps: string[];
  announced: HandoffRecord[];
} {
  const steps: string[] = [];
  const announced: HandoffRecord[] = [];
  const deps: HandoffDeps = {
    sessionId: () => "session-abc",
    isIdle: () => true,
    announce: async (rec) => {
      announced.push(rec);
      steps.push(`announce:${rec.verb}`);
    },
    closeBus: async () => {
      steps.push("closeBus");
    },
    exit: () => {
      steps.push("exit");
    },
    log: () => {},
    now: () => new Date("2026-06-19T00:00:00.000Z"),
    waitMs: async () => {},
    ...overrides,
  };
  return { deps, steps, announced };
}

test("onDrain runs the single-owner sequence in order: announce → close → exit", async () => {
  const { deps, steps, announced } = recordingDeps();
  const h = new Handoff(deps);
  assert.equal(h.isPending(), false);
  await h.onDrain("operator handing the session to lena-2");
  assert.equal(h.isPending(), true, "pending is set so the wake path stops taking work");
  assert.deepEqual(steps, ["announce:relinquished", "closeBus", "exit"], "release is announced, THEN the bus closes, THEN the process exits");
  assert.equal(announced.length, 1);
  assert.equal(announced[0]!.$type, HANDOFF_TYPE);
  assert.equal(announced[0]!.verb, VerbRelinquished);
  assert.equal(announced[0]!.session, "session-abc", "the relinquished announcement names the persisted session to resume");
  assert.equal(announced[0]!.reason, "operator handing the session to lena-2");
});

test("onDrain is idempotent: a second drain while winding down is a no-op", async () => {
  const { deps, steps } = recordingDeps();
  const h = new Handoff(deps);
  await h.onDrain("first");
  await h.onDrain("second"); // must NOT close/exit again
  assert.deepEqual(
    steps,
    ["announce:relinquished", "closeBus", "exit"],
    "a repeated drain does not double-close or double-exit",
  );
});

test("onDrain waits for the current turn to finish before releasing", async () => {
  // The agent is busy for the first two idle-polls, then goes idle. The wind-down
  // must not announce/close until then — a drain never truncates a turn mid-flight.
  let idleChecks = 0;
  const steps: string[] = [];
  const { deps } = recordingDeps({
    isIdle: () => {
      idleChecks++;
      return idleChecks > 2; // busy, busy, then idle
    },
    announce: async (rec) => {
      steps.push(`announce:${rec.verb}`);
    },
    closeBus: async () => {
      steps.push("closeBus");
    },
    exit: () => {
      steps.push("exit");
    },
  });
  const h = new Handoff(deps);
  await h.onDrain("wait for the turn");
  assert.ok(idleChecks >= 3, `polled idle until the turn finished (checks=${idleChecks})`);
  assert.deepEqual(steps, ["announce:relinquished", "closeBus", "exit"], "released only after going idle");
});

test("onDrain still releases even if the announce fails (close+exit are the real release)", async () => {
  const steps: string[] = [];
  const { deps } = recordingDeps({
    announce: async () => {
      throw new Error("bus publish failed");
    },
    closeBus: async () => {
      steps.push("closeBus");
    },
    exit: () => {
      steps.push("exit");
    },
  });
  const h = new Handoff(deps);
  await h.onDrain("announce will fail");
  assert.deepEqual(steps, ["closeBus", "exit"], "a failed announce never strands the worker owning the session");
});

test("announceAcquired emits the mirror announcement on a resume", async () => {
  const { deps, announced } = recordingDeps();
  const h = new Handoff(deps);
  await h.announceAcquired("session-abc", "re-spawned to resume");
  assert.equal(announced.length, 1);
  assert.equal(announced[0]!.verb, VerbAcquired);
  assert.equal(announced[0]!.session, "session-abc");
  assert.equal(h.isPending(), false, "acquiring a session does not put the new owner into a winding-down state");
});
