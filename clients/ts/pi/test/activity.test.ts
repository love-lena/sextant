// Unit tests for the pi.activity observability bridge (the spike's adjustment 3).
// They pin the record shapes a dash reads: turn markers, thinking + reply text
// pulled out of an assistant message, tool start/end with truncated args/result,
// and that a publish failure never throws into the agent.

import { test } from "node:test";
import assert from "node:assert/strict";
import { ActivityBridge, extractText, type ActivityRecord, type Publisher } from "../src/activity.js";
import type { JSONValue } from "@sextant/sdk";

// recordingPublisher captures every publish so a test can inspect the records.
function recordingPublisher(): { pub: Publisher; records: ActivityRecord[]; subjects: string[] } {
  const records: ActivityRecord[] = [];
  const subjects: string[] = [];
  const pub: Publisher = {
    async publish(subject: string, record: JSONValue) {
      subjects.push(subject);
      records.push(record as unknown as ActivityRecord);
    },
  };
  return { pub, records, subjects };
}

function bridge(pub: Publisher | undefined, previewMax = 600): ActivityBridge {
  return new ActivityBridge({
    publisher: () => pub,
    topicSubject: () => "msg.topic.pi.activity.SELF",
    previewMax,
    now: () => new Date("2026-06-19T00:00:00.000Z"),
  });
}

test("turn_start / turn_end emit turn markers on the activity subject", async () => {
  const { pub, records, subjects } = recordingPublisher();
  const b = bridge(pub);
  b.onTurnStart({ type: "turn_start", turnIndex: 0, timestamp: 0 } as never);
  b.onTurnEnd({ type: "turn_end", turnIndex: 0, message: { role: "assistant", content: [] }, toolResults: [] } as never);
  await tick();
  const kinds = records.map((r) => r.kind);
  assert.deepEqual(kinds, ["turn_start", "turn_end"], "an empty turn emits just the markers");
  assert.ok(subjects.every((s) => s === "msg.topic.pi.activity.SELF"));
  assert.equal(records[0]!.turnIndex, 0);
});

test("turn_end pulls thinking and reply text into their own records", async () => {
  const { pub, records } = recordingPublisher();
  const b = bridge(pub);
  const message = {
    role: "assistant",
    content: [
      { type: "thinking", thinking: "let me check the bus" },
      { type: "text", text: "Acknowledged." },
    ],
  };
  b.onTurnEnd({ type: "turn_end", turnIndex: 2, message, toolResults: [] } as never);
  await tick();
  const byKind = Object.fromEntries(records.map((r) => [r.kind, r]));
  assert.equal(byKind["thinking"]?.text, "let me check the bus");
  assert.equal(byKind["message"]?.text, "Acknowledged.");
  assert.equal(byKind["thinking"]?.turnIndex, 2);
  // Order: thinking, message, then the turn_end marker.
  assert.deepEqual(records.map((r) => r.kind), ["thinking", "message", "turn_end"]);
});

test("tool_start / tool_end carry name, id, args/result, and truncate", async () => {
  const { pub, records } = recordingPublisher();
  const b = bridge(pub, /* previewMax */ 10);
  b.onToolStart({ toolName: "bash", toolCallId: "tc1", args: { command: "echo hello-world-this-is-long" } } as never);
  b.onToolEnd({ toolName: "bash", toolCallId: "tc1", isError: false, result: "hello-world-this-is-long" } as never);
  await tick();
  const start = records.find((r) => r.kind === "tool_start")!;
  const end = records.find((r) => r.kind === "tool_end")!;
  assert.equal(start.tool, "bash");
  assert.equal(start.toolCallId, "tc1");
  assert.ok(start.args!.length <= 11 && start.args!.endsWith("…"), "args truncated to previewMax + ellipsis");
  assert.equal(end.isError, false);
  assert.ok(end.result!.endsWith("…"), "result truncated");
});

test("a publish failure never throws into the agent", async () => {
  const failing: Publisher = {
    async publish() {
      throw new Error("bus down");
    },
  };
  let captured: Error | undefined;
  const b = new ActivityBridge({
    publisher: () => failing,
    topicSubject: () => "msg.topic.pi.activity.SELF",
    previewMax: 600,
    onError: (e) => (captured = e),
  });
  // Must not throw synchronously.
  assert.doesNotThrow(() => b.onTurnStart({ type: "turn_start", turnIndex: 0, timestamp: 0 } as never));
  await tick();
  assert.equal(captured?.message, "bus down", "the error is routed to onError, not thrown");
});

test("no live publisher: emit is a silent no-op", () => {
  const b = bridge(undefined);
  assert.doesNotThrow(() => b.onTurnStart({ type: "turn_start", turnIndex: 0, timestamp: 0 } as never));
});

test("extractText tolerates string content and missing content", () => {
  assert.deepEqual(extractText({ content: "plain reply" }), { thinking: "", text: "plain reply" });
  assert.deepEqual(extractText({}), { thinking: "", text: "" });
  assert.deepEqual(extractText(undefined), { thinking: "", text: "" });
});

// tick lets the fire-and-forget publish promise settle.
function tick(): Promise<void> {
  return new Promise((r) => setTimeout(r, 0));
}
