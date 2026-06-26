// Unit tests for the canonical-JSON rule and the frame codec — the canonical
// edge cases the single shipped wire vector does not exercise. The fixtures are
// ported from Go's protocol/conformance/vector_test.go (TestCanonicalize) so the
// two implementations are pinned to the SAME number/escape/unicode rule.

import { test } from "node:test";
import assert from "node:assert/strict";
import { canonical, parseJSON, encodeHex, decodeHex } from "../src/wire/codec.js";
import { validateFrame, isValidULID, type Frame } from "../src/wire/frame.js";
import { checkEpoch, checkSkew, EpochError, SkewError, EPOCH } from "../src/wire/epoch.js";
import type { JSONValue } from "../src/types.js";

// asJSON re-types a Frame as a JSONValue for canonical() comparison. A Frame is
// a plain object whose fields are all JSON values; it just lacks the index
// signature JSONValue requires structurally.
function asJSON(f: Frame): JSONValue {
  return f as unknown as JSONValue;
}

// canonicalize(text) parses text (big-int-preserving) then re-emits the
// canonical form — the same compose the recorder/replayer uses.
function canon(text: string): string {
  return canonical(parseJSON(text));
}

test("canonicalization rule (ported from Go vector_test.go)", () => {
  const cases: Array<[string, string, string]> = [
    ["object keys sorted", `{"b":1,"a":2}`, `{"a":2,"b":1}`],
    ["nested keys sorted", `{"z":{"y":1,"x":2}}`, `{"z":{"x":2,"y":1}}`],
    ["whitespace stripped", "{\n  \"a\" : 1\n}", `{"a":1}`],
    ["arrays keep order", `[3,1,2]`, `[3,1,2]`],
    ["integers minimal", `{"n": 1.0}`, `{"n":1}`],
    ["trailing zero fraction", `{"n": 1.50}`, `{"n":1.5}`],
    ["exponent expanded", `{"n": 1e2}`, `{"n":100}`],
    ["negative integer", `{"n": -7.0}`, `{"n":-7}`],
    // Negative zero: a float literal canonicalizes to "-0" (Go's FormatFloat
    // parity), an integer literal to "0". Pinned to keep TS↔Go byte-identical.
    ["negative zero float", `{"n": -0.0}`, `{"n":-0}`],
    ["negative zero integer", `{"n": -0}`, `{"n":0}`],
    ["large integer preserved", `{"seq": 9007199254740993}`, `{"seq":9007199254740993}`],
    ["html chars not escaped", `{"t":"a<b>c&d"}`, `{"t":"a<b>c&d"}`],
    ["unicode preserved", `{"name":"héllo→世界"}`, `{"name":"héllo→世界"}`],
    ["control char escaped", "{\"t\":\"a\\nb\"}", `{"t":"a\\nb"}`],
    ["null/bool", `{"a":null,"b":true,"c":false}`, `{"a":null,"b":true,"c":false}`],
    ["empty object", `{}`, `{}`],
  ];
  for (const [name, input, want] of cases) {
    assert.equal(canon(input), want, name);
  }
});

test("canonicalization is idempotent", () => {
  const once = canon(`{"b":[1,2,{"d":4,"c":3}],"a":"x"}`);
  const twice = canonical(parseJSON(once));
  assert.equal(once, twice);
});

test("large integer beyond 2^53 survives as exact digits (bigint)", () => {
  const v = parseJSON(`{"seq":9007199254740993}`) as { seq: bigint };
  assert.equal(typeof v.seq, "bigint");
  assert.equal(v.seq, 9007199254740993n);
  assert.equal(canonical(v), `{"seq":9007199254740993}`);

  // A very large integer (far beyond float precision) keeps every digit.
  const huge = `{"n":123456789012345678901234567890}`;
  assert.equal(canon(huge), huge);
});

test("safe-range integers stay as JS numbers (not bigint)", () => {
  const v = parseJSON(`{"a":42,"b":-7,"c":9007199254740991}`) as { a: unknown; b: unknown; c: unknown };
  assert.equal(typeof v.a, "number");
  assert.equal(typeof v.b, "number");
  assert.equal(typeof v.c, "number"); // 2^53-1 is the boundary, still safe
});

test("HTML characters are not escaped (SetEscapeHTML(false) parity)", () => {
  assert.equal(canon(`{"t":"<div>&copy;</div>"}`), `{"t":"<div>&copy;</div>"}`);
});

test("arrays are never key-sorted; only object keys are", () => {
  assert.equal(canon(`[{"b":1,"a":2},{"d":3,"c":4}]`), `[{"a":2,"b":1},{"c":4,"d":3}]`);
});

test("frame codec round-trips a message frame to exactly five core keys", () => {
  const f: Frame = {
    id: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
    author: "01ARZ3NDEKTSV4RRFFQ69G5FAW",
    kind: "message",
    epoch: 1,
    record: { $type: "chat.message", text: "hi" },
  };
  const hex = encodeHex(f);
  const back = decodeHex(hex);
  assert.equal(canonical(asJSON(back)), canonical(asJSON(f)));
  // A message frame carries exactly author/epoch/id/kind/record — no
  // artifact-only keys, even if set on the input object.
  const decodedKeys = Object.keys(back).sort();
  assert.deepEqual(decodedKeys, ["author", "epoch", "id", "kind", "record"]);
});

test("frame codec omits artifact-only keys on a message but keeps them on an artifact", () => {
  const msg: Frame = {
    id: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
    author: "01ARZ3NDEKTSV4RRFFQ69G5FAW",
    kind: "message",
    epoch: 1,
    record: { x: 1 },
    revision: 5, // should be dropped on a message frame
    createdAt: "2026-01-01T00:00:00Z",
  };
  const back = decodeHex(encodeHex(msg));
  assert.equal(back.revision, undefined);
  assert.equal(back.createdAt, undefined);

  const art: Frame = {
    id: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
    author: "01ARZ3NDEKTSV4RRFFQ69G5FAW",
    kind: "artifact",
    epoch: 1,
    record: { title: "v1" },
    revision: 2,
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-02T00:00:00Z",
  };
  const backArt = decodeHex(encodeHex(art));
  assert.equal(backArt.revision, 2);
  assert.equal(backArt.createdAt, "2026-01-01T00:00:00Z");
  assert.equal(backArt.updatedAt, "2026-01-02T00:00:00Z");
});

test("ULID validation accepts a well-formed ULID and rejects malformed ones", () => {
  assert.ok(isValidULID("01ARZ3NDEKTSV4RRFFQ69G5FAV"));
  assert.ok(!isValidULID("01ARZ3NDEKTSV4RRFFQ69G5FA")); // 25 chars
  assert.ok(!isValidULID("01ARZ3NDEKTSV4RRFFQ69G5FAVX")); // 27 chars
  assert.ok(!isValidULID("01ARZ3NDEKTSV4RRFFQ69G5FAI")); // 'I' not in Crockford
  assert.ok(!isValidULID("81ARZ3NDEKTSV4RRFFQ69G5FAV")); // timestamp overflow (first char > 7)
});

test("validateFrame enforces the wire contract", () => {
  const good: Frame = {
    id: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
    author: "01ARZ3NDEKTSV4RRFFQ69G5FAW",
    kind: "message",
    epoch: 1,
    record: { x: 1 },
  };
  assert.doesNotThrow(() => validateFrame(good));
  assert.throws(() => validateFrame({ ...good, id: "not-a-ulid" }), /invalid id/);
  assert.throws(() => validateFrame({ ...good, author: "" }), /empty author/);
  assert.throws(() => validateFrame({ ...good, kind: "bogus" }), /unknown kind/);
});

test("checkEpoch is exact and throws an EpochError on mismatch", () => {
  assert.doesNotThrow(() => checkEpoch(EPOCH, EPOCH));
  assert.throws(() => checkEpoch(2, EPOCH), EpochError);
});

test("checkSkew quarantines a frame whose ULID time is far from bus time", () => {
  // A ULID minted at ~now, compared against a bus time 10 minutes off.
  const nowUlid = "01ARZ3NDEKTSV4RRFFQ69G5FAV"; // fixed time below; just needs to parse
  // Build a ULID with a known timestamp: 2026-06-19T00:00:00Z.
  const t = Date.UTC(2026, 5, 19, 0, 0, 0);
  const id = ulidWithMillis(t);
  assert.doesNotThrow(() => checkSkew(id, new Date(t + 60_000), 5 * 60 * 1000)); // 1m within 5m
  assert.throws(() => checkSkew(id, new Date(t + 10 * 60_000), 5 * 60 * 1000), SkewError); // 10m > 5m
  assert.ok(isValidULID(nowUlid));
});

// ulidWithMillis builds a valid ULID string whose embedded timestamp is the
// given millisecond value (random low bits), for the skew test.
function ulidWithMillis(ms: number): string {
  const CROCKFORD = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";
  const chars: string[] = new Array(26).fill("0");
  let t = ms;
  for (let i = 9; i >= 0; i--) {
    chars[i] = CROCKFORD[t % 32]!;
    t = Math.floor(t / 32);
  }
  for (let i = 10; i < 26; i++) chars[i] = "0";
  return chars.join("");
}
