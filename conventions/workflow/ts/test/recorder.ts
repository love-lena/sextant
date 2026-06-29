// The TS recording client for the workflow convention: a fake Ops that CAPTURES the
// primitive operations the run/v1 verbs perform instead of issuing them to a real bus —
// the TS peer of Go's conformance.Recorder. The run/v1 publish verbs are publish-only,
// so the recorder captures publishes; the ordered list of captured ops IS the
// transcript a vector pins.

import type { JSONValue } from "@sextant/sdk";
import type { Ops } from "../src/index.js";

// Op is one captured primitive operation, in the on-disk vector shape. Only the
// fields an operation uses are populated — the populated SET is part of the contract.
export interface Op {
  op: string;
  subject?: string;
  name?: string;
  payload?: JSONValue;
  expectedRev?: number;
}

// Recorder captures the primitive bus operations a run/v1 verb performs. It implements
// Ops (publish), so the verb runs unchanged against it.
export class Recorder implements Ops {
  private readonly captured: Op[] = [];

  // operations returns the captured transcript, in call order.
  operations(): Op[] {
    return this.captured;
  }

  async publish(subject: string, record: JSONValue): Promise<void> {
    this.captured.push({ op: "message.publish", subject, payload: structuredClone(record) as JSONValue });
  }
}
