// The live co-equality scenario (TASK-239 AC#3 — the real proof, not two suites
// independently green): on ONE real bus, the TS review convention and the Go review
// convention write/read the SAME artifact's review block, and the record shapes are
// asserted BYTE-IDENTICAL in both directions. This is what "the protocol is
// language-neutral" means operationally: two languages, one review-block shape,
// identical wire records. (The approve->met closed loop is pinned separately by the
// shared setReviewApprove.json conformance vector both suites replay.)
//
// The proof drives the REAL write path on each side — the TS setReview verb
// (get -> compare-and-set) and the Go review.SetReview via a helper binary
// (test/gohelper) — never a hand-rolled artifact write. "Byte-identical" is
// measured with the canonical-JSON rule (FORMAT.md): the SDK's `canonical` on the
// TS side and protocol/conformance.Canonicalize on the Go side, the same rule.
//
// Gated on the Go toolchain being present (skip-with-reason when `go` is not on
// PATH) so the unit/conformance tests still run everywhere.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { connect, canonical, type Client, type JSONValue } from "@sextant/sdk";
import { setReview } from "../src/index.js";
import { startBus, goAvailable, type Bus } from "./harness.js";

const skip = !goAvailable();
const skipReason = "the `go` toolchain is not on PATH (the live co-equality scenario needs the real Go bus + convention)";

// fixedNow is the verdict timestamp both sides stamp; BY is the verdict author both
// sides use, so a Go-written and a TS-written record carry identical review blocks
// and canonicalize identically (the doc record carries no artifact-name field).
const fixedNow = "2026-06-19T00:00:00Z";
const BY = "coequal-reviewer";

// fixedDoc is the shared starting record both languages seed — the exact TS peer of
// the Go helper's fixedDoc(): a producer-marked document awaiting review.
function fixedDoc(): JSONValue {
  return { $type: "doc", title: "the brief", body: "the body", review: { state: "review" } };
}

function obj(v: JSONValue): { [k: string]: JSONValue } {
  return v as { [k: string]: JSONValue };
}

let bus: Bus;
let tsCreds: string;
let goCreds: string;

before(() => {
  if (skip) return;
  bus = startBus();
  tsCreds = bus.mint("ts-review", "agent").credsPath;
  goCreds = bus.mint("go-review", "agent").credsPath;
});

after(() => {
  if (skip || !bus) return;
  bus.stop();
});

test("co-equality A: a review verdict the TS convention writes is read byte-identical by the Go convention", { skip: skip && skipReason }, async () => {
  const c: Client = await connect({ credsPath: tsCreds, url: bus.url });
  try {
    const name = "ts-written";
    await c.createArtifact(name, fixedDoc());
    const res = await setReview(c, { name, state: "approved", by: BY, now: fixedNow });
    assert.equal(res.review, "approved", "the TS verb persisted the verdict");

    const tsCanon = canonical((await c.getArtifact(name)).record);

    const go = bus.runGo(["read", name], goCreds);
    assert.equal(go.code, 0, `go read failed: ${go.stderr}`);
    const goCanon = go.stdout.trim();

    assert.equal(goCanon, tsCanon, "Go reads byte-identical bytes to what TS wrote");
    // The review block carries the operator's verdict.
    const rb = obj(obj((await c.getArtifact(name)).record)["review"]!);
    assert.equal(rb["state"], "approved");
    assert.equal(rb["by"], BY);
    assert.equal(rb["at"], fixedNow);
    console.error(`[co-equality A] TS wrote == Go read (byte-identical):\n  ${goCanon}`);
  } finally {
    await c.close();
  }
});

test("co-equality B: a review verdict the Go convention writes is read byte-identical by the TS convention", { skip: skip && skipReason }, async () => {
  const c: Client = await connect({ credsPath: tsCreds, url: bus.url });
  try {
    const name = "go-written";
    const seedR = bus.runGo(["seed", name], goCreds);
    assert.equal(seedR.code, 0, `go seed failed: ${seedR.stderr}`);
    const setR = bus.runGo(["setreview", name, "approved", BY], goCreds);
    assert.equal(setR.code, 0, `go setreview failed: ${setR.stderr}`);
    const goCanon = setR.stdout.trim();

    const tsCanon = canonical((await c.getArtifact(name)).record);

    assert.equal(tsCanon, goCanon, "TS reads byte-identical bytes to what Go wrote");
    console.error(`[co-equality B] Go wrote == TS read (byte-identical):\n  ${tsCanon}`);
  } finally {
    await c.close();
  }
});

test("co-equality round-trip: a TS-written verdict and a Go-written verdict are the SAME canonical record", { skip: skip && skipReason }, async () => {
  // Both sides start from the identical fixedDoc and apply the identical verdict
  // (same state/by/now), and the doc record carries no artifact-name field — so the
  // two records must be byte-identical EXCEPT for review.rev, which records the
  // artifact's own revision at verdict time and is assigned by the bus's global KV
  // sequence (so two different artifacts legitimately differ). We normalize that one
  // bus-incidental field out; everything the CONVENTION determines must match.
  const c: Client = await connect({ credsPath: tsCreds, url: bus.url });
  try {
    const tsName = "rt-ts";
    await c.createArtifact(tsName, fixedDoc());
    await setReview(c, { name: tsName, state: "approved", by: BY, now: fixedNow });
    const tsRecord = (await c.getArtifact(tsName)).record;

    const goName = "rt-go";
    assert.equal(bus.runGo(["seed", goName], goCreds).code, 0);
    const goSet = bus.runGo(["setreview", goName, "approved", BY], goCreds);
    assert.equal(goSet.code, 0, `go setreview failed: ${goSet.stderr}`);
    const goRecord = JSON.parse(goSet.stdout.trim()) as JSONValue;

    assert.equal(
      canonical(withoutReviewRev(tsRecord)),
      canonical(withoutReviewRev(goRecord)),
      "TS write == Go write (byte-identical review records, modulo the bus-assigned review.rev)",
    );
    // And the bus-assigned rev is present and positive on each side (the convention
    // DID record the revision it CAS'd against — it just differs per artifact).
    assert.ok((obj(obj(tsRecord)["review"]!)["rev"] as number) > 0, "TS recorded a positive review.rev");
    assert.ok((obj(obj(goRecord)["review"]!)["rev"] as number) > 0, "Go recorded a positive review.rev");
  } finally {
    await c.close();
  }
});

// withoutReviewRev returns a deep copy of a doc record with review.rev removed, so
// a cross-artifact comparison ignores the bus-assigned revision (the one value the
// convention does not determine) while still pinning every other field.
function withoutReviewRev(record: JSONValue): JSONValue {
  const copy = structuredClone(record) as { [k: string]: JSONValue };
  const rb = copy["review"];
  if (rb && typeof rb === "object" && !Array.isArray(rb)) {
    delete (rb as { [k: string]: JSONValue })["rev"];
  }
  return copy;
}
