// Unit tests for the wake / back-pressure policy (WakeQueue) — the spike's AC#3
// adjustment, exercised as pure logic with no pi and no bus. These pin the
// behaviour the spike characterised against a real flood: bounded, drop-oldest,
// a reserved DM slot, burst-coalescing, and ordered one-per-turn draining.

import { test } from "node:test";
import assert from "node:assert/strict";
import { WakeQueue, type Pending } from "../src/wake.js";

// mkPending builds a Pending whose deliver records that it ran, so a test can
// assert which frames were delivered and in what order.
function mkPending(
  q: WakeQueue,
  delivered: string[],
  opts: { direct?: boolean; topic?: string; author?: string; id?: string },
): Pending {
  const id = opts.id ?? `${opts.topic ?? "t"}#${opts.author ?? "a"}`;
  return {
    direct: opts.direct ?? false,
    topic: opts.topic ?? "msg.topic.crew",
    author: opts.author ?? "AUTHOR1",
    seq: q.nextSeq(),
    deliver: (count) => delivered.push(`${id}x${count}`),
  };
}

test("topic queue is bounded and drops the oldest under flood", () => {
  const q = new WakeQueue({ maxBuffered: 4, coalesceWindowMs: 0 });
  // 10 distinct topic frames (distinct authors so coalescing is irrelevant here).
  for (let i = 0; i < 10; i++) {
    q.enqueue(mkPending(q, [], { author: `A${i}`, id: `f${i}` }));
  }
  assert.equal(q.bufferedTopic(), 4, "topic queue capped at maxBuffered");
  assert.equal(q.droppedTotal(), 6, "the 6 oldest were dropped");
});

test("a direct (DM) frame is never dropped and is delivered before topic frames", () => {
  const delivered: string[] = [];
  const q = new WakeQueue({ maxBuffered: 2, coalesceWindowMs: 0 });
  // Fill the topic queue past its cap.
  for (let i = 0; i < 5; i++) q.enqueue(mkPending(q, delivered, { author: `A${i}`, id: `topic${i}` }));
  // A DM arrives — it must go to the reserved class, not be dropped.
  q.enqueue(mkPending(q, delivered, { direct: true, topic: "msg.client.SELF", author: "BOSS", id: "dm" }));
  assert.equal(q.bufferedDirect(), 1, "the DM is buffered in the reserved class");
  assert.equal(q.bufferedTopic(), 2, "topic queue still capped");

  // Draining delivers the DM FIRST (reserved class beats topic class).
  const first = q.takeNext();
  assert.ok(first);
  first!.p.deliver(first!.coalescedCount);
  assert.equal(delivered[0], "dmx1", "the DM drains before any topic frame");
});

test("a same-author/same-topic burst coalesces into one entry with a count", () => {
  const delivered: string[] = [];
  const q = new WakeQueue({ maxBuffered: 16, coalesceWindowMs: 1000 });
  for (let i = 0; i < 5; i++) {
    q.enqueue(mkPending(q, delivered, { topic: "msg.topic.crew", author: "CHATTY", id: `m${i}` }));
  }
  assert.equal(q.bufferedTopic(), 1, "five frames from one author on one topic coalesce to one entry");
  const next = q.takeNext();
  assert.ok(next);
  assert.equal(next!.coalescedCount, 5, "the coalesced count reflects the burst size");
  next!.p.deliver(next!.coalescedCount);
  // The freshest deliver closure wins (the last frame, m4).
  assert.equal(delivered[0], "m4x5", "the latest frame's content is what gets delivered");
});

test("distinct authors on the same topic do NOT coalesce", () => {
  const q = new WakeQueue({ maxBuffered: 16, coalesceWindowMs: 1000 });
  q.enqueue(mkPending(q, [], { topic: "msg.topic.crew", author: "A", id: "a" }));
  q.enqueue(mkPending(q, [], { topic: "msg.topic.crew", author: "B", id: "b" }));
  assert.equal(q.bufferedTopic(), 2, "different authors stay separate entries");
});

test("the queue drains in order, one per take, and reports empty", () => {
  const delivered: string[] = [];
  const q = new WakeQueue({ maxBuffered: 16, coalesceWindowMs: 0 });
  q.enqueue(mkPending(q, delivered, { author: "A", id: "first" }));
  q.enqueue(mkPending(q, delivered, { author: "B", id: "second" }));
  assert.equal(q.isEmpty(), false);
  let n = q.takeNext();
  n!.p.deliver(n!.coalescedCount);
  n = q.takeNext();
  n!.p.deliver(n!.coalescedCount);
  assert.equal(q.isEmpty(), true, "queue empties after draining everything");
  assert.deepEqual(delivered, ["firstx1", "secondx1"], "FIFO within the topic class");
});
