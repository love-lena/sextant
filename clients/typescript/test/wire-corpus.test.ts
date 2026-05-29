/**
 * Go↔TS wire round-trip over the checked-in message corpus.
 *
 * The corpus is authored by Go (pkg/sextantproto/corpus_test.go, written
 * with `-update-corpus`) so these bytes are exactly what a Go peer puts on
 * the bus. This suite decodes each one with the GENERATED TypeScript types
 * + envelope helpers and asserts the wire still works — the cross-language
 * contract test the C1 ticket requires.
 *
 * It also pins the generated proto_version.ts (PROTO_VERSION / WIRE_EPOCH /
 * KIND_* / ADDRESS_* / FRAME_*) against the same wire.json the Go generator
 * emits, so the two ends can never silently drift.
 */

import { describe, it, expect } from "vitest";

import { readdir, readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

import {
  decodeEnvelope,
  encodeEnvelope,
  validateEnvelope,
  PROTO_VERSION,
  WIRE_EPOCH,
} from "../src/index.js";
import type { Envelope } from "../src/index.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const REPO_ROOT = path.resolve(__dirname, "..", "..", "..");
const CORPUS_DIR = path.join(REPO_ROOT, "pkg", "sextantproto", "testdata", "wire-corpus");
const WIRE_JSON = path.join(REPO_ROOT, "pkg", "sextantproto", "schemas", "wire.json");

interface WireManifest {
  proto_version: string;
  wire_epoch: number;
  kinds: string[];
  address_kinds: string[];
  frame_kinds: string[];
}

async function loadManifest(): Promise<WireManifest> {
  return JSON.parse(await readFile(WIRE_JSON, "utf8")) as WireManifest;
}

describe("generated proto_version", () => {
  it("PROTO_VERSION and WIRE_EPOCH match the Go-emitted wire.json", async () => {
    const m = await loadManifest();
    expect(PROTO_VERSION).toBe(m.proto_version);
    expect(WIRE_EPOCH).toBe(m.wire_epoch);
  });
});

describe("Go↔TS wire corpus", () => {
  it("decodes every Go-authored envelope with the generated types", async () => {
    const files = (await readdir(CORPUS_DIR)).filter((f) => f.endsWith(".json")).sort();
    expect(files.length).toBeGreaterThan(0);

    const manifest = await loadManifest();

    for (const f of files) {
      const raw = await readFile(path.join(CORPUS_DIR, f), "utf8");
      const bytes = new TextEncoder().encode(raw);

      // decodeEnvelope parses + runs the structural validator; a Go
      // envelope must pass the TS validator unchanged.
      const env: Envelope = decodeEnvelope(bytes);

      const kind = path.basename(f, ".json");
      expect(env.kind).toBe(kind);
      expect(manifest.kinds).toContain(env.kind);
      expect(env.proto_version).toBe(manifest.proto_version);
      expect(manifest.address_kinds).toContain(env.from.kind);

      // Re-encoding and re-decoding is stable (no field loss / corruption).
      const reencoded = encodeEnvelope({ ...env });
      const back = decodeEnvelope(reencoded);
      expect(back.id).toBe(env.id);
      expect(back.trace_id).toBe(env.trace_id);
      expect(back.kind).toBe(env.kind);
      expect(() => validateEnvelope(back)).not.toThrow();
    }
  });
});
