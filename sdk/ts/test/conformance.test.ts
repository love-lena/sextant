// The conformance replay: the TS frame codec replays the SAME wire vectors the
// Go SDK does (protocol/conformance/vectors/wire/*.json), in both directions,
// per FORMAT.md. Passing this suite for the protocol epoch is what makes the TS
// client co-equal (ADR-0041). The vectors are read directly from the protocol
// tree — never copied.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { canonical, decodeHex, encodeHex, parseJSON, decode, hexToBytes } from "../src/wire/codec.js";
import { wireVectorsDir } from "./repoRoot.js";
import type { JSONValue } from "../src/types.js";

interface WireVector {
  epoch: number;
  description?: string;
  frame: JSONValue;
  bytes: string;
}

function loadWireVectors(): Array<{ path: string; vector: WireVector }> {
  const dir = wireVectorsDir();
  const files = readdirSync(dir).filter((f) => f.endsWith(".json"));
  if (files.length === 0) {
    throw new Error(`no wire vectors found under ${dir}`);
  }
  return files.map((f) => {
    const path = join(dir, f);
    return { path, vector: JSON.parse(readFileSync(path, "utf8")) as WireVector };
  });
}

test("the wire conformance vectors are discovered", () => {
  const vectors = loadWireVectors();
  assert.ok(vectors.length >= 1, "expected at least one wire vector");
});

for (const { path, vector } of loadWireVectors()) {
  test(`wire vector ${path.split("/").slice(-2).join("/")} round-trips both directions`, () => {
    // Decode direction: decode(bytes) deep-equals vector.frame (FORMAT.md). We
    // compare under the canonical rule, since the vector's frame is stored
    // canonically and the codec may produce keys in any order.
    const frame = decodeHex(vector.bytes);
    assert.equal(
      canonical(frame as unknown as JSONValue),
      canonical(vector.frame),
      "decode(bytes) must equal vector.frame",
    );

    // Encode direction: canonical(encode(frame)) equals the vector bytes — the
    // direction that pins cross-language parity. The simplest equivalent
    // (FORMAT.md): bytesToHex(encode(frame)) === vector.bytes.toLowerCase().
    assert.equal(
      encodeHex(frame),
      vector.bytes.toLowerCase(),
      "canonical(encode(frame)) must equal the vector bytes",
    );

    // And re-canonicalizing the vector's raw bytes (decoded to JSON) must equal
    // the encoder's canonical output — the FORMAT.md statement of the encode
    // direction, expressed on the parsed value.
    const rawText = new TextDecoder().decode(hexToBytes(vector.bytes));
    assert.equal(canonical(parseJSON(rawText)), canonical(decode(hexToBytes(vector.bytes)) as unknown as JSONValue));

    // The vector pins epoch 1 (the current protocol epoch).
    assert.equal(vector.epoch, 1, "the shipped wire vector is pinned to epoch 1");
    assert.equal(frame.epoch, 1, "the decoded frame carries epoch 1");
  });
}
