// The TS recording client: a fake Ops that CAPTURES the primitive operations a
// goal verb performs instead of issuing them to a real bus — the TS peer of Go's
// conformance.Recorder (sdk/conformance/recorder.go). A verb written
// against the goals.Ops seam runs unchanged against a Recorder; the ordered list
// of captured ops IS the transcript a conformance vector pins.
//
// This is what makes the TS suite co-equal: it replays the SAME vector files the
// Go suite does and asserts byte-identical operations under the canonical-JSON
// rule (FORMAT.md, ADR-0041).

import type { JSONValue } from "@sextant/sdk";
import type { Ops } from "../src/index.js";

// Op is one captured primitive operation, in the on-disk vector shape
// (protocol/conformance/vectors/<convention>/<verb>.json): an op name, the
// subject (message ops) or name (artifact ops), an optional payload (the record or
// args), and the compare-and-set revision on an artifact.update. Only the fields an
// operation uses are populated — the populated SET is part of the contract.
export interface Op {
  op: string;
  subject?: string;
  name?: string;
  payload?: JSONValue;
  expectedRev?: number;
}

interface SeededArtifact {
  record: JSONValue;
  revision: number;
}

// Recorder captures the primitive bus operations a verb performs. It implements
// Ops, so a verb runs unchanged against it. Each call appends one entry to its ops
// in call order; that ordered slice is the transcript the runner compares against.
//
// A Recorder is single-use and not safe for concurrent verbs; a verb is a
// straight-line sequence of operations, recorded on one execution.
export class Recorder implements Ops {
  private readonly captured: Op[] = [];
  // seeded is the recorder's stub artifact store: enough state for a read-then-
  // write verb to see a plausible prior value and revision without a real bus.
  // State a verb DEPENDS on before its first write is loaded with seedArtifact
  // (recording setup, not transcript). The recorder is a stub, not a faithful CAS
  // store.
  private readonly seeded = new Map<string, SeededArtifact>();

  // seedArtifact pre-loads an artifact so a read-then-write verb (the common goal
  // pattern: get the goal, mutate a criterion, update) sees a realistic prior value
  // during recording. It does not appear in the transcript; it is recording setup
  // mirroring the bus state a verb would find live. The peer of Go's
  // Recorder.SeedArtifact.
  seedArtifact(name: string, record: JSONValue, revision: number): void {
    this.seeded.set(name, { record: clone(record), revision });
  }

  // operations returns the captured transcript, in call order.
  operations(): Op[] {
    return this.captured;
  }

  // --- Ops implementation: the recorded primitive surface ---

  // getArtifact records an artifact.get and returns seeded state if present. A get
  // is itself an observable operation (a verb that reads before writing emits it),
  // so it is captured. With no seeded state it returns null/0 — enough for a verb
  // to proceed; seed state for read-dependent verbs.
  async getArtifact(name: string): Promise<{ record: JSONValue; revision: number }> {
    this.captured.push({ op: "artifact.get", name });
    const s = this.seeded.get(name);
    if (s) {
      return { record: clone(s.record), revision: s.revision };
    }
    return { record: null, revision: 0 };
  }

  // updateArtifact records an artifact.update (carrying the compare-and-set
  // revision) and returns the advanced stub revision.
  async updateArtifact(name: string, record: JSONValue, expectedRev: number): Promise<number> {
    this.captured.push({
      op: "artifact.update",
      name,
      payload: clone(record),
      expectedRev,
    });
    const next = expectedRev + 1;
    this.seeded.set(name, { record: clone(record), revision: next });
    return next;
  }

  // publish records a message.publish. Subject and the record are captured as the
  // operation's canonical payload.
  async publish(subject: string, record: JSONValue): Promise<void> {
    this.captured.push({ op: "message.publish", subject, payload: clone(record) });
  }
}

// clone deep-copies a JSON value so the recorder's captured payload can't be
// mutated by a verb reusing a reference (the peer of Go's cloneRaw).
function clone(v: JSONValue): JSONValue {
  return v === undefined ? null : (structuredClone(v) as JSONValue);
}
