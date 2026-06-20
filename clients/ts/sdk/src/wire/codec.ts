// The frame codec and the canonical-JSON rule (FORMAT.md). This is the contract
// that makes the TS client co-equal (ADR-0041): the codec must encode a frame to
// the exact canonical bytes of the wire vectors and decode those bytes back to
// the frame. The two directions are verified against
// protocol/conformance/vectors/wire/*.json — the SAME files the Go SDK replays.
//
// The canonicalization rule, reproduced exactly from FORMAT.md:
//   1. Object keys sorted by Unicode code point, ascending, recursively.
//   2. No insignificant whitespace.
//   3. JSON-standard string escaping with HTML escaping OFF (escape U+0000–U+001F,
//      '"', '\'; do NOT escape '<', '>', '&') — exactly what JSON.stringify does.
//   4. Numbers in minimal canonical form: integer-valued → exact digits (1.0→1);
//      large integers beyond IEEE-754 (> 2^53-1) keep EXACT digits; non-integer →
//      shortest round-trip (1.50→1.5, 1e2→100).
//   5. UTF-8 throughout; Unicode verbatim (not \u-escaped).
//   6. null/true/false as-is; arrays preserve order (never sorted).
//
// The big-integer case (rule 4) is why this module ships its own JSON parser:
// JSON.parse rounds integers > 2^53-1, and JSON.stringify cannot emit them. The
// parser yields a bigint for any out-of-safe-range integer literal, and the
// serializer emits a bigint as bare digits — so 9007199254740993 survives.

import type { JSONValue } from "../types.js";
import { type Frame, KIND_MESSAGE } from "./frame.js";

// ---------------------------------------------------------------------------
// canonical — the FORMAT.md rule
// ---------------------------------------------------------------------------

// canonical returns the canonical-JSON encoding of value as a string. It is the
// single rule the Go and TS codecs must agree on byte-for-byte.
export function canonical(value: JSONValue): string {
  let out = "";
  writeCanonical(value, (s) => {
    out += s;
  });
  return out;
}

function writeCanonical(v: JSONValue, emit: (s: string) => void): void {
  if (v === null || v === undefined) {
    emit("null");
    return;
  }
  switch (typeof v) {
    case "boolean":
      emit(v ? "true" : "false");
      return;
    case "bigint":
      // A big integer beyond IEEE-754 precision: bare digits, exact (no quotes,
      // no `n` suffix). JS never round-trips it through a float here.
      emit(v.toString(10));
      return;
    case "number":
      emit(canonicalNumber(v));
      return;
    case "string":
      // JSON.stringify on a string does exactly the JSON-standard escaping with
      // HTML escaping off (JS does not escape <, >, &), and emits Unicode in the
      // BMP verbatim — matching Go's SetEscapeHTML(false). This is rules 3 and 5.
      emit(JSON.stringify(v));
      return;
    case "object":
      if (Array.isArray(v)) {
        emit("[");
        for (let i = 0; i < v.length; i++) {
          if (i > 0) emit(",");
          writeCanonical(v[i] as JSONValue, emit); // arrays preserve order
        }
        emit("]");
        return;
      }
      writeObject(v as { [k: string]: JSONValue }, emit);
      return;
    default:
      throw new Error(`canonical: unsupported value of type ${typeof v}`);
  }
}

function writeObject(obj: { [k: string]: JSONValue }, emit: (s: string) => void): void {
  // Object.keys().sort() sorts by UTF-16 code unit, which agrees with Unicode
  // code point for the Basic Multilingual Plane — sufficient for all expected
  // lexicon keys (FORMAT.md). Values are never sorted, only keys.
  const keys = Object.keys(obj).sort();
  emit("{");
  for (let i = 0; i < keys.length; i++) {
    const k = keys[i]!;
    if (i > 0) emit(",");
    emit(JSON.stringify(k)); // keys use the same string escaping as values
    emit(":");
    writeCanonical(obj[k] as JSONValue, emit);
  }
  emit("}");
}

// canonicalNumber normalizes a finite JS number to its minimal canonical text
// (rule 4). An integer-valued number emits its exact integer digits with no
// fraction; a non-integer uses the shortest round-trip form. A JS number is
// IEEE-754, so an integer here is always within 2^53-1 — the > 2^53-1 case is
// carried by bigint (see the bigint arm above), never reaching here.
function canonicalNumber(n: number): string {
  if (!Number.isFinite(n)) {
    throw new Error(`canonical: non-finite number ${n} is not valid JSON`);
  }
  if (Number.isInteger(n)) {
    // Negative zero that arrived as a FLOAT literal (-0.0) canonicalizes to "-0"
    // — that is what Go's strconv.FormatFloat('g', -1, 64) emits, and the parser
    // preserves the distinction (an integer literal -0 parses to +0, a float
    // literal -0.0 parses to -0), so the two implementations agree byte-for-byte.
    if (Object.is(n, -0)) {
      return "-0";
    }
    // Exact integer digits, no ".0".
    return n.toString(10);
  }
  // Shortest round-trip float form. JSON.stringify of a finite non-integer
  // number is exactly the ECMAScript Number-to-String shortest form (1.5, 100),
  // which is what Go's strconv.FormatFloat('g', -1, 64) matches.
  return JSON.stringify(n);
}

// ---------------------------------------------------------------------------
// A big-integer-preserving JSON parser
// ---------------------------------------------------------------------------

// parseJSON parses text into a JSONValue, yielding a bigint for any integer
// literal outside the IEEE-754 safe range so its exact digits survive. Every
// other number becomes a JS number. It is a strict recursive-descent parser; it
// throws on trailing or malformed input.
export function parseJSON(text: string): JSONValue {
  const p = new Parser(text);
  const v = p.parseValue();
  p.skipWhitespace();
  if (!p.atEnd()) {
    throw new Error(`json: trailing characters at offset ${p.offset}`);
  }
  return v;
}

class Parser {
  offset = 0;
  constructor(private readonly s: string) {}

  atEnd(): boolean {
    return this.offset >= this.s.length;
  }

  skipWhitespace(): void {
    while (this.offset < this.s.length) {
      const c = this.s[this.offset]!;
      if (c === " " || c === "\t" || c === "\n" || c === "\r") {
        this.offset++;
      } else {
        break;
      }
    }
  }

  parseValue(): JSONValue {
    this.skipWhitespace();
    if (this.atEnd()) throw new Error("json: unexpected end of input");
    const c = this.s[this.offset]!;
    switch (c) {
      case "{":
        return this.parseObject();
      case "[":
        return this.parseArray();
      case '"':
        return this.parseString();
      case "t":
        this.expect("true");
        return true;
      case "f":
        this.expect("false");
        return false;
      case "n":
        this.expect("null");
        return null;
      default:
        if (c === "-" || (c >= "0" && c <= "9")) {
          return this.parseNumber();
        }
        throw new Error(`json: unexpected character ${JSON.stringify(c)} at offset ${this.offset}`);
    }
  }

  private expect(lit: string): void {
    if (this.s.slice(this.offset, this.offset + lit.length) !== lit) {
      throw new Error(`json: expected ${JSON.stringify(lit)} at offset ${this.offset}`);
    }
    this.offset += lit.length;
  }

  private parseObject(): { [k: string]: JSONValue } {
    this.offset++; // consume {
    const obj: { [k: string]: JSONValue } = {};
    this.skipWhitespace();
    if (this.s[this.offset] === "}") {
      this.offset++;
      return obj;
    }
    for (;;) {
      this.skipWhitespace();
      if (this.s[this.offset] !== '"') {
        throw new Error(`json: expected object key at offset ${this.offset}`);
      }
      const key = this.parseString();
      this.skipWhitespace();
      if (this.s[this.offset] !== ":") {
        throw new Error(`json: expected ':' at offset ${this.offset}`);
      }
      this.offset++; // consume :
      obj[key] = this.parseValue();
      this.skipWhitespace();
      const sep = this.s[this.offset];
      if (sep === ",") {
        this.offset++;
        continue;
      }
      if (sep === "}") {
        this.offset++;
        return obj;
      }
      throw new Error(`json: expected ',' or '}' at offset ${this.offset}`);
    }
  }

  private parseArray(): JSONValue[] {
    this.offset++; // consume [
    const arr: JSONValue[] = [];
    this.skipWhitespace();
    if (this.s[this.offset] === "]") {
      this.offset++;
      return arr;
    }
    for (;;) {
      arr.push(this.parseValue());
      this.skipWhitespace();
      const sep = this.s[this.offset];
      if (sep === ",") {
        this.offset++;
        continue;
      }
      if (sep === "]") {
        this.offset++;
        return arr;
      }
      throw new Error(`json: expected ',' or ']' at offset ${this.offset}`);
    }
  }

  private parseString(): string {
    // Delegate the escape handling to JSON.parse over just the string token,
    // which is the spec-faithful unescaper. Scan to the matching unescaped
    // quote first so the token boundary is exact.
    const start = this.offset;
    this.offset++; // consume opening "
    for (;;) {
      if (this.offset >= this.s.length) {
        throw new Error("json: unterminated string");
      }
      const c = this.s[this.offset]!;
      if (c === "\\") {
        this.offset += 2; // skip the escape and its following char
        continue;
      }
      if (c === '"') {
        this.offset++; // consume closing "
        break;
      }
      this.offset++;
    }
    return JSON.parse(this.s.slice(start, this.offset)) as string;
  }

  private parseNumber(): number | bigint {
    const start = this.offset;
    if (this.s[this.offset] === "-") this.offset++;
    while (this.isDigit(this.s[this.offset])) this.offset++;
    let isInteger = true;
    if (this.s[this.offset] === ".") {
      isInteger = false;
      this.offset++;
      while (this.isDigit(this.s[this.offset])) this.offset++;
    }
    const e = this.s[this.offset];
    if (e === "e" || e === "E") {
      isInteger = false;
      this.offset++;
      const sign = this.s[this.offset];
      if (sign === "+" || sign === "-") this.offset++;
      while (this.isDigit(this.s[this.offset])) this.offset++;
    }
    const lit = this.s.slice(start, this.offset);
    if (isInteger) {
      // An integer literal: keep its EXACT value. If it fits the IEEE-754 safe
      // range, a JS number suffices; otherwise a bigint preserves every digit
      // (FORMAT.md rule 4 — 9007199254740993 must not round).
      const big = BigInt(lit);
      if (big >= BigInt(Number.MIN_SAFE_INTEGER) && big <= BigInt(Number.MAX_SAFE_INTEGER)) {
        return Number(big);
      }
      return big;
    }
    return Number(lit);
  }

  private isDigit(c: string | undefined): boolean {
    return c !== undefined && c >= "0" && c <= "9";
  }
}

// ---------------------------------------------------------------------------
// Frame ↔ bytes
// ---------------------------------------------------------------------------

// encode serializes a frame to its canonical wire bytes (UTF-8 of the canonical
// JSON). For a MESSAGE frame the artifact-only keys are omitted, so the frame
// carries exactly the five core keys; for an ARTIFACT frame the present
// artifact-only keys are included. The returned bytes are byte-identical across
// languages — this is what pins cross-language parity.
export function encode(f: Frame): Uint8Array {
  return new TextEncoder().encode(canonical(frameToObject(f)));
}

// encodeHex is encode followed by lowercase-hex, matching the vector `bytes`
// form.
export function encodeHex(f: Frame): string {
  return bytesToHex(encode(f));
}

// frameToObject builds the plain object the canonical encoder serializes,
// including only the keys the frame actually carries (Go `omitempty` semantics:
// the artifact-only keys are present only on an artifact frame with non-zero
// values).
function frameToObject(f: Frame): { [k: string]: JSONValue } {
  const obj: { [k: string]: JSONValue } = {
    id: f.id,
    author: f.author,
    kind: f.kind,
    epoch: f.epoch,
    record: f.record,
  };
  if (f.kind !== KIND_MESSAGE) {
    if (f.revision !== undefined && f.revision !== 0) obj["revision"] = f.revision;
    if (f.createdAt !== undefined && f.createdAt !== "") obj["createdAt"] = f.createdAt;
    if (f.updatedAt !== undefined && f.updatedAt !== "") obj["updatedAt"] = f.updatedAt;
  }
  return obj;
}

// decode parses canonical wire bytes into a Frame. It is the pure codec — it
// maps the JSON object onto the Frame shape but does NOT validate the frame's
// semantics (ULID id, author, kind, record). That mirrors the Go codec
// (wire.Decode is a plain unmarshal; Frame.Validate is a separate step the
// delivery path applies — see validateFrame). The conformance vectors carry
// recognizable placeholder ids ("…AUTHOR", "…FRAME") that are deliberately not
// real ULIDs, so the codec direction must not reject them. The receive path
// quarantines a malformed frame by calling validateFrame after decode.
export function decode(bytes: Uint8Array): Frame {
  const text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  const v = parseJSON(text);
  if (v === null || typeof v !== "object" || Array.isArray(v)) {
    throw new Error("wire: frame must be a JSON object");
  }
  return objectToFrame(v as { [k: string]: JSONValue });
}

// decodeHex is hex → bytes → decode.
export function decodeHex(hex: string): Frame {
  return decode(hexToBytes(hex));
}

function objectToFrame(o: { [k: string]: JSONValue }): Frame {
  const id = asString(o["id"], "id");
  const author = asString(o["author"], "author");
  const kind = asString(o["kind"], "kind");
  const epoch = asNumber(o["epoch"], "epoch");
  const record = o["record"];
  if (record === undefined) {
    throw new Error("wire: frame has no record");
  }
  const f: Frame = { id, author, kind, epoch, record };
  if (o["revision"] !== undefined) f.revision = asNumber(o["revision"], "revision");
  if (o["createdAt"] !== undefined) f.createdAt = asString(o["createdAt"], "createdAt");
  if (o["updatedAt"] !== undefined) f.updatedAt = asString(o["updatedAt"], "updatedAt");
  return f;
}

function asString(v: JSONValue, field: string): string {
  if (typeof v !== "string") {
    throw new Error(`wire: frame.${field} must be a string`);
  }
  return v;
}

function asNumber(v: JSONValue, field: string): number {
  if (typeof v === "number") return v;
  if (typeof v === "bigint") return Number(v);
  throw new Error(`wire: frame.${field} must be a number`);
}

// ---------------------------------------------------------------------------
// hex
// ---------------------------------------------------------------------------

// bytesToHex lowercase-hex encodes a byte array (matching the vector `bytes`
// form). Node's Buffer is available on the target runtime; using it keeps the
// hex path dependency-free and fast.
export function bytesToHex(b: Uint8Array): string {
  return Buffer.from(b).toString("hex");
}

// hexToBytes decodes lowercase (or any-case) hex to bytes.
export function hexToBytes(hex: string): Uint8Array {
  if (hex.length % 2 !== 0) {
    throw new Error("wire: hex string has an odd length");
  }
  const buf = Buffer.from(hex, "hex");
  // Buffer.from with "hex" stops at the first non-hex byte rather than throwing;
  // a length mismatch means the input was not valid hex.
  if (buf.length !== hex.length / 2) {
    throw new Error("wire: invalid hex string");
  }
  return new Uint8Array(buf);
}
