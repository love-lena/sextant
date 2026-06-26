// The TS recording client for the review convention: a fake Ops that CAPTURES the
// primitive operations a verb performs instead of issuing them to a real bus — the
// TS peer of Go's conformance.Recorder (sdk/conformance/recorder.go). A verb
// written against the review Ops seam (get/update/publish) runs unchanged against a
// Recorder; the ordered list of captured ops IS the transcript a vector pins. It is
// multi-artifact (the approve closed loop reads/writes a separate goal.<id>), so it
// mirrors the goals recorder.
//
// This is what makes the TS suite co-equal: it replays the SAME vector files the Go
// suite does and asserts byte-identical operations under the canonical-JSON rule
// (FORMAT.md, ADR-0041).

import type { JSONValue } from "@sextant/sdk";
import type { Ops } from "../src/index.js";

// Op is one captured primitive operation, in the on-disk vector shape: an op name,
// the subject (message ops) or name (artifact ops), an optional payload, and the
// compare-and-set revision on an artifact.update. Only the fields an operation uses
// are populated — the populated SET is part of the contract.
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
export class Recorder implements Ops {
  private readonly captured: Op[] = [];
  private readonly seeded = new Map<string, SeededArtifact>();

  // seedArtifact pre-loads an artifact so a read-then-write verb sees a realistic
  // prior value during recording. It does not appear in the transcript; it is
  // recording setup mirroring the bus state a verb would find live. The peer of
  // Go's Recorder.SeedArtifact.
  seedArtifact(name: string, record: JSONValue, revision: number): void {
    this.seeded.set(name, { record: clone(record), revision });
  }

  // operations returns the captured transcript, in call order.
  operations(): Op[] {
    return this.captured;
  }

  // --- Ops implementation: the recorded primitive surface ---

  async getArtifact(name: string): Promise<{ record: JSONValue; revision: number }> {
    this.captured.push({ op: "artifact.get", name });
    const s = this.seeded.get(name);
    if (s) {
      return { record: clone(s.record), revision: s.revision };
    }
    return { record: null, revision: 0 };
  }

  async updateArtifact(name: string, record: JSONValue, expectedRev: number): Promise<number> {
    this.captured.push({ op: "artifact.update", name, payload: clone(record), expectedRev });
    const next = expectedRev + 1;
    this.seeded.set(name, { record: clone(record), revision: next });
    return next;
  }

  async publish(subject: string, record: JSONValue): Promise<void> {
    this.captured.push({ op: "message.publish", subject, payload: clone(record) });
  }
}

// clone deep-copies a JSON value so the recorder's captured payload can't be
// mutated by a verb reusing a reference (the peer of Go's cloneRaw).
function clone(v: JSONValue): JSONValue {
  return v === undefined ? null : (structuredClone(v) as JSONValue);
}
