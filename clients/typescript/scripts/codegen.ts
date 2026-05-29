/**
 * codegen.ts — regenerate src/types.generated.ts from JSON Schemas.
 *
 * Source of truth: pkg/sextantproto/schemas/*.json (regenerated from Go
 * structs via `go generate ./pkg/sextantproto/...`).
 *
 * Output: src/types.generated.ts (a single committed file). Run this
 * script after the Go schemas change. CI re-runs it and asserts the
 * committed file is in sync.
 *
 * Quirks:
 *   - invopop/jsonschema reflects Go uuid.UUID as a fixed-length integer
 *     array, but on the wire UUIDs travel as canonical lowercase strings.
 *     We rewrite the UUID definition to `string` before running json2ts.
 *   - invopop/jsonschema reflects sextantproto.Timestamp as an empty
 *     object; the wire form is an RFC 3339 string. Rewrite to `string`.
 *   - Names collide across schema files (every file redefines `UUID`,
 *     `Address`, etc.). We merge $defs from every schema into one big
 *     $defs object before generating, so we get exactly one canonical
 *     emission per type.
 */

import { readdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { compile, type JSONSchema } from "json-schema-to-typescript";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const REPO_ROOT = path.resolve(__dirname, "..", "..", "..");
const SCHEMA_DIR = path.join(REPO_ROOT, "pkg", "sextantproto", "schemas");
const WIRE_MANIFEST = path.join(SCHEMA_DIR, "wire.json");
const OUT_FILE = path.resolve(__dirname, "..", "src", "types.generated.ts");
const PROTO_VERSION_FILE = path.resolve(__dirname, "..", "src", "proto_version.ts");

interface SchemaFile {
  $schema?: string;
  $id?: string;
  $ref?: string;
  $defs?: Record<string, JSONSchema>;
}

async function loadSchemas(): Promise<SchemaFile[]> {
  const files = (await readdir(SCHEMA_DIR))
    // wire.json is the contract-substrate manifest (proto_version,
    // WireEpoch, closed enums), not a JSON Schema — it drives
    // proto_version.ts, not the type bundle.
    .filter((f) => f.endsWith(".json") && f !== "wire.json")
    .sort();
  const out: SchemaFile[] = [];
  for (const f of files) {
    const raw = await readFile(path.join(SCHEMA_DIR, f), "utf8");
    out.push(JSON.parse(raw) as SchemaFile);
  }
  return out;
}

/**
 * Merge all $defs across schema files into a single mega-schema. The
 * top-level $ref/$id is dropped — we want every type emitted, not just
 * one rooted at the per-file $ref.
 *
 * Collision policy: identical bodies are coalesced; mismatched bodies
 * throw — that would mean two schemas drifted from each other and the
 * Go side should be the fix.
 */
function mergeDefs(schemas: SchemaFile[]): Record<string, JSONSchema> {
  const merged: Record<string, JSONSchema> = {};
  for (const s of schemas) {
    if (!s.$defs) continue;
    for (const [name, def] of Object.entries(s.$defs)) {
      const existing = merged[name];
      if (existing === undefined) {
        merged[name] = def;
        continue;
      }
      const a = JSON.stringify(existing);
      const b = JSON.stringify(def);
      if (a !== b) {
        throw new Error(
          `codegen: conflicting definitions for ${name}; reconcile the Go structs that produce these schemas`,
        );
      }
    }
  }
  return merged;
}

/**
 * Rewrite the known leaf types to their wire representation.
 *
 * UUID, Timestamp, time.Time (which surfaces as a date-time string
 * already), and any json.RawMessage payload (`payload: true`) are not
 * Go-shaped on the wire; they're strings or arbitrary JSON.
 */
function rewriteLeafTypes(defs: Record<string, JSONSchema>): void {
  // UUID: array-of-bytes in the schema, string on the wire.
  if (defs["UUID"]) {
    defs["UUID"] = {
      type: "string",
      format: "uuid",
      description: "UUID (canonical lowercase string form on the wire).",
    };
  }
  // Timestamp: the Go custom marshaller emits an RFC 3339 string.
  if (defs["Timestamp"]) {
    defs["Timestamp"] = {
      type: "string",
      format: "date-time",
      description:
        "RFC 3339 timestamp with microsecond precision (matches Go sextantproto.Timestamp).",
    };
  }
}

interface WireManifest {
  proto_version: string;
  wire_epoch: number;
  kinds: string[];
  address_kinds: string[];
  frame_kinds: string[];
}

/**
 * Emit src/proto_version.ts from the Go-authored wire.json manifest.
 *
 * This is what kills the last hand-sync: PROTO_VERSION, the WireEpoch
 * compatibility key (WIRE_EPOCH), and the closed-enum constant sets
 * (KIND_*, ADDRESS_*, FRAME_*) are all derived from the same source the
 * Go side stamps, so editing a Go const + `go generate` regenerates them
 * with no hand edits.
 */
async function generateProtoVersion(): Promise<void> {
  const manifest = JSON.parse(await readFile(WIRE_MANIFEST, "utf8")) as WireManifest;

  // Wire value "agent_frame" → const name "KIND_AGENT_FRAME".
  const constName = (value: string): string => value.toUpperCase().replace(/[^A-Z0-9]+/g, "_");

  const block = (
    comment: string,
    prefix: string,
    values: string[],
  ): string => {
    const lines = values.map((v) => `export const ${prefix}_${constName(v)} = ${JSON.stringify(v)};`);
    // The empty-string LifecycleSource etc. would collapse to "_" — none
    // of the closed enums here include an empty value, but guard anyway.
    return [`// ${comment}`, ...lines].join("\n");
  };

  const body = [
    "/* eslint-disable */",
    "/**",
    " * Generated by clients/typescript/scripts/codegen.ts from",
    " * pkg/sextantproto/schemas/wire.json (itself generated from the Go",
    " * source of truth in pkg/sextantproto).",
    " *",
    " * DO NOT EDIT by hand — re-run `npm run codegen` (or `go generate",
    " * ./...`) after the Go wire constants change.",
    " */",
    "",
    "/** Envelope protocol version emitted by this client (matches Go ProtoVersion). */",
    `export const PROTO_VERSION = ${JSON.stringify(manifest.proto_version)};`,
    "",
    "/**",
    " * WireEpoch — the machine-checked bus compatibility key (RFC §5.8).",
    " * Matches Go sextantproto.WireEpoch; the CI schema-compat gate fails a",
    " * breaking wire change that did not bump it.",
    " */",
    `export const WIRE_EPOCH = ${manifest.wire_epoch};`,
    "",
    block("Recognized envelope kinds. Mirrors sextantproto.Kind.", "KIND", manifest.kinds),
    "",
    block("Recognized Address kinds. Mirrors sextantproto.AddressKind.", "ADDRESS", manifest.address_kinds),
    "",
    block("Recognized agent frame kinds. Mirrors sextantproto.FrameKind.", "FRAME", manifest.frame_kinds),
    "",
    "/** Every recognized AddressKind, for membership checks. */",
    `export const ADDRESS_KINDS = ${JSON.stringify(manifest.address_kinds)} as const;`,
    "",
  ].join("\n");

  await writeFile(PROTO_VERSION_FILE, body, "utf8");
  console.log(`wrote ${PROTO_VERSION_FILE} (${body.length} bytes)`);
}

async function main(): Promise<void> {
  await generateProtoVersion();

  const schemas = await loadSchemas();
  const defs = mergeDefs(schemas);
  rewriteLeafTypes(defs);

  // Build the mega-schema. We give it a stable $id and explicitly
  // declare a body so json2ts has a top-level shape — but we mark every
  // top-level definition as "to be emitted" by referencing them all
  // from a synthetic root object. json2ts emits every named $def
  // anyway, which is what we want.
  const root: JSONSchema = {
    $schema: "https://json-schema.org/draft/2020-12/schema",
    $id: "https://github.com/love-lena/sextant/clients/typescript/types-bundle",
    title: "SextantProtoBundle",
    type: "object",
    additionalProperties: false,
    properties: {},
    $defs: defs,
  };

  const compiled = await compile(root, "SextantProtoBundle", {
    bannerComment: [
      "/* eslint-disable */",
      "/**",
      " * Generated by clients/typescript/scripts/codegen.ts.",
      " * DO NOT EDIT by hand — re-run `npm run codegen` after the source",
      " * JSON Schemas under pkg/sextantproto/schemas/ change.",
      " */",
    ].join("\n"),
    additionalProperties: false,
    declareExternallyReferenced: true,
    enableConstEnums: false,
    strictIndexSignatures: true,
    style: {
      semi: true,
      singleQuote: false,
      trailingComma: "all",
      printWidth: 100,
    },
    unreachableDefinitions: true,
  });

  await writeFile(OUT_FILE, compiled, "utf8");
  console.log(`wrote ${OUT_FILE} (${compiled.length} bytes)`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
