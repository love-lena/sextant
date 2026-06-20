// The wire atom: the bus-stamped wrapper around a typed lexicon record. The
// record is user space (the client supplies it); the frame is bus space (the
// bus produces id, author, and the rest). Mirrors protocol/wire/frame.go and
// protocol/wire/validate.go — the structure and the validation are the protocol
// contract every co-equal client reproduces (ADR-0006, ADR-0041).

import type { JSONValue } from "../types.js";

// Frame kinds. Kind discriminates a frame: a message in flight or an artifact
// at rest.
export const KIND_MESSAGE = "message";
export const KIND_ARTIFACT = "artifact";

// Frame is the wire atom — exactly the wire keys. A MESSAGE frame carries the
// five core keys (author, epoch, id, kind, record); the artifact-only keys
// (revision, createdAt, updatedAt) are OMITTED when absent, matching Go's
// `omitempty`. The codec never emits them on a message.
export interface Frame {
  id: string; // bus-minted ULID (26-char Crockford base32)
  author: string; // authenticated client id
  kind: string; // "message" | "artifact"
  epoch: number; // must equal EPOCH = 1
  record: JSONValue; // opaque lexicon; non-empty, valid JSON
  revision?: number; // artifact-only; omit on messages
  createdAt?: string; // artifact-only RFC3339; omit on messages
  updatedAt?: string; // artifact-only RFC3339; omit on messages
}

// Crockford base32 alphabet (excludes I, L, O, U), as used by ULID. The
// timestamp portion of a valid ULID can encode at most 0x7FFFFFFFFFFF ms (48
// bits), so the first character must be 0–7. Mirrors the parse oklog/ulid does.
const CROCKFORD = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";
const DECODE: Record<string, number> = {};
for (let i = 0; i < CROCKFORD.length; i++) {
  const c = CROCKFORD[i]!;
  DECODE[c] = i;
  DECODE[c.toLowerCase()] = i;
}

// parseULIDMillis validates a 26-char Crockford-base32 ULID and returns the
// embedded millisecond timestamp (the high 48 bits). It throws on a malformed
// id, mirroring the strict parse the Go SDK relies on (ulid.Parse).
export function parseULIDMillis(id: string): number {
  if (id.length !== 26) {
    throw new Error(`wire: invalid ULID ${JSON.stringify(id)}: length ${id.length}, want 26`);
  }
  for (let i = 0; i < 26; i++) {
    if (DECODE[id[i]!] === undefined) {
      throw new Error(`wire: invalid ULID ${JSON.stringify(id)}: bad character at ${i}`);
    }
  }
  // The first character holds the top 3 bits of the 48-bit timestamp; a valid
  // ULID timestamp fits 48 bits, so it must be 0–7 (anything larger overflows).
  if (DECODE[id[0]!]! > 7) {
    throw new Error(`wire: invalid ULID ${JSON.stringify(id)}: timestamp overflow`);
  }
  // Decode the first 10 characters (50 bits, of which the top 48 are the
  // timestamp) into a millisecond value. 48 bits fits in a JS safe integer.
  let ms = 0;
  for (let i = 0; i < 10; i++) {
    ms = ms * 32 + DECODE[id[i]!]!;
  }
  return ms;
}

// isValidULID reports whether id parses as a ULID, without throwing.
export function isValidULID(id: string): boolean {
  try {
    parseULIDMillis(id);
    return true;
  } catch {
    return false;
  }
}

// validateFrame checks a frame is structurally well-formed — a parseable ULID
// id, a non-empty author, a known kind, and a non-empty record — mirroring
// wire/validate.go's Frame.Validate. It does NOT check the epoch: that is
// contextual (see checkEpoch), because durable streams outlive epochs. Throws a
// descriptive error on the first violation.
export function validateFrame(f: Frame): void {
  if (!isValidULID(f.id)) {
    throw new Error(`wire: invalid id: ${JSON.stringify(f.id)}`);
  }
  if (f.author === "") {
    throw new Error("wire: empty author");
  }
  if (f.kind !== KIND_MESSAGE && f.kind !== KIND_ARTIFACT) {
    throw new Error(`wire: unknown kind ${JSON.stringify(f.kind)}`);
  }
  // A present record. Go's check is `len(record) == 0 || !json.Valid(record)`
  // on the raw bytes — i.e. the record must be present and parse as JSON. After
  // decoding, any value other than an absent field is a valid, non-empty JSON
  // value (even `{}`, `[]`, `null`, which Go accepts as valid non-zero bytes),
  // so the faithful TS equivalent is: the field must be present (not undefined).
  if (f.record === undefined) {
    throw new Error("wire: record must be non-empty valid JSON");
  }
}
