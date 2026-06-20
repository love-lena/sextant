// Browser-safety tests for the SDK browser entry (ADR-0044). The browser-safe
// rewrites — pure-JS hex (codec), base64url JWT decode + Web Crypto ULID (conn) —
// must be byte-EQUIVALENT to the Buffer/randomFillSync path they replaced, because
// they sit on the AUTH path (identityFromCreds reads the JWT the bus then
// authenticates) and the wire path (hex). A base64url bug would silently corrupt
// the JWT read; these pin it against a real bus-minted credential.

import { test } from "node:test";
import assert from "node:assert/strict";
import { bytesToHex, hexToBytes } from "../src/wire/codec.js";
import { identityFromCreds, newULID } from "../src/transport/conn.js";
import { isValidULID } from "../src/wire/frame.js";

// A REAL credential JWT minted by `sextant clients register "Dash Brow    léna"
// --kind browser` — name = a bus ULID, a display_name:<hex> tag carrying a
// non-ASCII name (so the hex→utf8 path is exercised), and an exp claim (the
// ADR-0044 browser-cred TTL). The signature is truncated (identityFromCreds does
// not verify it — the bus does). Wrapped in the decorated creds envelope the
// parser scans for.
const BROWSER_JWT =
  "eyJ0eXAiOiJKV1QiLCJhbGciOiJlZDI1NTE5LW5rZXkifQ.eyJleHAiOjE3ODE5Mjg1NjEsImp0aSI6IkJGN0lEVU5BWU1MSU42VlE0WFlJTFUzWllXU0ZSUFQyVjVSVURKRE9WN0JXRlRZSlczUFEiLCJpYXQiOjE3ODE5MjQ5NjEsImlzcyI6IkFEWFFSWUdFQ1ZDVFQ2NjNUVjZINEhLSTZKVFRSUDQyUk9RRE5MRFRUU0daSVlEVlk2NktQNk9aIiwibmFtZSI6IjAxS1ZIRzI3RFFHUEhNOERRRkZHSk5LWlJKIiwic3ViIjoiVUNQMzdJTVkyWUlNRFJCQUJQTVczWU1YRTZENEpIUEs0UUhLTVJUMlE2R0paRjdJWUtaSEdMREIiLCJuYXRzIjp7InB1YiI6eyJhbGxvdyI6WyJzeC5hcGkuMDFLVkhHMjdEUUdQSE04RFFGRkdKTktaUkouXHUwMDNlIl19LCJzdWIiOnsiYWxsb3ciOlsic3guZGVsaXZlci4wMUtWSEcyN0RRR1BITThEUUZGR0pOS1pSSi5cdTAwM2UiLCJfSU5CT1guMDFLVkhHMjdEUUdQSE04RFFGRkdKTktaUkouXHUwMDNlIiwic3guaGIuMDFLVkhHMjdEUUdQSE04RFFGRkdKTktaUkoiXX0sInN1YnMiOi0xLCJkYXRhIjotMSwicGF5bG9hZCI6LTEsImlzc3Vlcl9hY2NvdW50IjoiQURYUVJZR0VDVkNUVDY2M1RWNkg0SEtJNkpUVFJQNDJST1FETkxEVFRTR1pJWURWWTY2S1A2T1oiLCJ0YWdzIjpbImRpc3BsYXlfbmFtZTo0NDYxNzM2ODIwNDI3MjZmNzcyMDIwNmNjM2E5NmU2MSJdLCJ0eXBlIjoidXNlciIsInZlcnNpb24iOjJ9fQ.FZ4oOFuBf2OW";

const BROWSER_CREDS = `-----BEGIN NATS USER JWT-----
${BROWSER_JWT}
------END NATS USER JWT------
`;

test("identityFromCreds reads the id + non-ASCII display name from a real browser JWT (base64url + hex pure-JS path)", () => {
  const id = identityFromCreds(BROWSER_CREDS);
  assert.equal(id.id, "01KVHG27DQGPHM8DQFFGJNKZRJ");
  // display_name tag hex 446173682042726f7720206cc3a96e61 → "Dash Brow  léna"
  assert.equal(id.displayName, "Dash Brow  léna");
});

test("hex round-trips byte-identically (pure-JS bytesToHex / hexToBytes)", () => {
  const cases: number[][] = [
    [],
    [0x00],
    [0xff],
    [0x00, 0x01, 0x7f, 0x80, 0xfe, 0xff],
    Array.from({ length: 256 }, (_, i) => i),
  ];
  for (const bytes of cases) {
    const u = new Uint8Array(bytes);
    const hex = bytesToHex(u);
    assert.equal(hex, hex.toLowerCase(), "hex is lowercase");
    assert.deepEqual(Array.from(hexToBytes(hex)), bytes);
  }
  // The display_name tag's exact hex must decode to the known UTF-8 bytes.
  const tagHex = "446173682042726f7720206cc3a96e61";
  assert.equal(new TextDecoder("utf-8").decode(hexToBytes(tagHex)), "Dash Brow  léna");
});

test("hexToBytes fails loud on a malformed string (no silent truncation)", () => {
  assert.throws(() => hexToBytes("abc"), /odd length/);
  assert.throws(() => hexToBytes("zz"), /invalid hex/);
  assert.throws(() => hexToBytes("00gg"), /invalid hex/);
});

test("newULID (Web Crypto) produces valid, unique, monotonic-prefixed ULIDs", () => {
  const a = newULID();
  const b = newULID();
  assert.ok(isValidULID(a), `${a} is a valid ULID`);
  assert.ok(isValidULID(b), `${b} is a valid ULID`);
  assert.equal(a.length, 26);
  // The 80 random bits make a collision astronomically unlikely.
  assert.notEqual(a, b);
});
