// The wake / back-pressure policy (the spike's AC#3 adjustment), as pure logic
// over a small queue so it is unit-testable without pi or a bus. The extension
// (index.ts) owns the side effects — opening the client, calling pi.sendMessage —
// and delegates the "what should we deliver next, and what do we drop" decision
// here.
//
// The policy, restated from the spike findings:
//   - A wake is "come look at the bus", NOT at-least-once delivery. The durable
//     record lives on the bus; the agent can read a topic to recover anything
//     this queue drops. That is what licenses dropping under flood.
//   - When the agent is busy, inbound frames buffer in a BOUNDED queue; on
//     overflow the OLDEST topic frame is dropped (the freshest signal wins).
//   - A RESERVED slot protects direct address (the inbox / DMs): a topic flood
//     can fill the topic queue to the cap, but a DM still gets through — it is
//     never dropped to make room for a topic frame, and it is delivered first.
//   - A same-author/same-topic burst COALESCES into a single "N new on <topic>"
//     wake instead of N turns (configurable window; 0 disables).
//   - The queue drains ONE per turn_end: delivering one wake re-triggers a turn
//     whose own turn_end flushes the next, so turns never stack unbounded.

// Pending is one buffered item awaiting delivery.
export interface Pending {
  // direct is true for a frame addressed straight to this agent (its inbox) —
  // the reserved-slot, never-dropped, delivered-first class.
  direct: boolean;
  // topic is the bus subject the frame arrived on (for coalescing + the banner).
  topic: string;
  // author is the bus-stamped frame author (for coalescing + trust-tiering).
  author: string;
  // seq orders arrivals so coalescing can report the freshest and a stable count.
  seq: number;
  // deliver carries the side-effecting closure the extension supplies — it holds
  // the frame + how to render and inject it. The queue never inspects content
  // (it stays opaque); it only schedules.
  deliver: (coalescedCount: number) => void;
  // coalesced counts how many same-author/same-topic frames this entry stands
  // for (set by the queue as it coalesces a burst). Undefined means 1.
  coalesced?: number;
}

// WakeQueueOptions configures the queue.
export interface WakeQueueOptions {
  maxBuffered: number; // bound on the topic queue (direct frames are unbounded-but-coalesced)
  coalesceWindowMs: number; // group same-author/same-topic bursts; 0 disables
  now?: () => number; // injectable clock for tests
}

// WakeDecision is what enqueue/flush tells the caller happened, so the extension
// can trace it (the spike's observability) without the queue doing I/O.
export interface WakeDecision {
  action: "deliver" | "buffer" | "coalesce" | "drop-oldest-then-buffer";
  bufferedTopic: number;
  bufferedDirect: number;
  droppedTotal: number;
}

// WakeQueue is the bounded, drop-oldest, reserved-DM-slot, coalescing buffer.
// Single-threaded by construction (it lives inside one pi extension instance);
// no locking needed.
export class WakeQueue {
  private readonly opts: Required<WakeQueueOptions>;
  private readonly direct: Pending[] = []; // reserved class — never dropped
  private readonly topic: Pending[] = []; // bounded, drop-oldest
  private dropped = 0;
  private seqCounter = 0;

  constructor(opts: WakeQueueOptions) {
    this.opts = {
      maxBuffered: opts.maxBuffered,
      coalesceWindowMs: opts.coalesceWindowMs,
      now: opts.now ?? (() => Date.now()),
    };
  }

  nextSeq(): number {
    return ++this.seqCounter;
  }

  droppedTotal(): number {
    return this.dropped;
  }

  bufferedDirect(): number {
    return this.direct.length;
  }

  bufferedTopic(): number {
    return this.topic.length;
  }

  isEmpty(): boolean {
    return this.direct.length === 0 && this.topic.length === 0;
  }

  // enqueue buffers a pending item while the agent is busy and returns the
  // decision. A direct (inbox/DM) frame goes to the reserved class and is never
  // dropped. A topic frame goes to the bounded class; on overflow the oldest
  // topic frame is dropped first. Within a class, a same-author/same-topic frame
  // already queued is coalesced (its count bumps; the newer deliver closure
  // replaces the older so the freshest content wins).
  enqueue(p: Pending): WakeDecision {
    const cls = p.direct ? this.direct : this.topic;

    if (this.opts.coalesceWindowMs > 0) {
      const existing = cls.find((q) => q.topic === p.topic && q.author === p.author);
      if (existing) {
        existing.seq = p.seq;
        existing.deliver = p.deliver;
        // mark coalesced by stashing a count on the closure boundary — we track
        // the count via a parallel field so flush can report it.
        existing.coalesced = (existing.coalesced ?? 1) + 1;
        return this.decision("coalesce");
      }
    }

    if (!p.direct && this.topic.length >= this.opts.maxBuffered) {
      this.topic.shift(); // drop oldest topic frame — the freshest signal wins
      this.dropped++;
      this.topic.push(p);
      return this.decision("drop-oldest-then-buffer");
    }

    cls.push(p);
    return this.decision("buffer");
  }

  // takeNext removes and returns the next item to deliver, or undefined when the
  // queue is empty. Direct frames (the reserved class) are delivered before topic
  // frames so a flood can never starve direct address.
  takeNext(): { p: Pending; coalescedCount: number } | undefined {
    const p = this.direct.shift() ?? this.topic.shift();
    if (!p) return undefined;
    return { p, coalescedCount: p.coalesced ?? 1 };
  }

  private decision(action: WakeDecision["action"]): WakeDecision {
    return {
      action,
      bufferedTopic: this.topic.length,
      bufferedDirect: this.direct.length,
      droppedTotal: this.dropped,
    };
  }
}
