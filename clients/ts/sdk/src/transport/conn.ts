// The connection layer: identity from the credential, the Wire API call envelope,
// a sub-id generator, and the connect options the two dialers share. Mirrors the
// Go SDK's call.go + the Connect identity resolution in client.go.
//
// This module is BROWSER-SAFE (ADR-0044): it imports no node:* and no transport
// package, so both the Node SDK entry (index.ts, over `nats`/TCP) and the browser
// entry (browser.ts, over `nats.ws`/wss) share it. The Node-only sites — the TCP
// dialer and the bus.json/.creds file reads — live in transport/node.ts, behind
// the Dialer seam below; the browser supplies its credsText + ws URL directly.

// Type-only import from `nats`: with verbatimModuleSyntax an `import type` emits
// NO runtime require, so this module stays browser-safe (no `nats`/TCP code in the
// browser bundle). The browser dialer's nats.ws NatsConnection is structurally the
// same shape — Synadia's two clients share the protocol layer.
import type { NatsConnection, Msg } from "nats";
import { callSubject, inboxPrefix } from "./callsubjects.js";
import { canonical, parseJSON, hexToBytes } from "../wire/codec.js";
import type { JSONValue } from "../types.js";

// Dialer opens a NatsConnection to url, authenticating as id with credsText. The
// two SDK entries inject their transport-specific dialer (Node: `nats`/TCP;
// browser: `nats.ws`/wss); shared code never imports a transport directly, so the
// browser bundle pulls in no `node:*`. Both dialers build their connection options
// from dialConnectOptions so the only difference is the import source.
export type Dialer = (url: string, credsText: string, id: string) => Promise<NatsConnection>;

// ConnectOptions configures the Node connect() (and connectIssuer()). All optional
// except credsPath. Mirrors Go's Options. The browser entry has its own option
// shape (BrowserConnectOptions) since it takes credsText + a ws url, not a path.
export interface ConnectOptions {
  credsPath: string; // path to the .creds file (required) — the client's own identity
  url?: string; // bus NATS URL; wins over connInfoPath
  connInfoPath?: string; // bus.json discovery file (fallback)
  skewToleranceMs?: number; // default 300_000 (5m)
  log?: (msg: string, ...a: unknown[]) => void; // default console.error
  heartbeatIntervalMs?: number; // default 15_000
  heartbeatFreshnessMs?: number; // default 45_000
  requestTimeoutMs?: number; // per-call request timeout; default 30_000
  dial?: Dialer; // the transport dialer; the Node entry defaults it to dialNats
}

// CoreConnectOptions is the credential-resolved option shape the shared connect()
// core consumes: credsText (already read — the Node entry reads the file, the
// browser is handed it) plus the tunables. It is ConnectOptions minus the
// Node-only path/discovery fields, which the entry has already resolved.
export interface CoreConnectOptions {
  skewToleranceMs?: number;
  log?: (msg: string, ...a: unknown[]) => void;
  heartbeatIntervalMs?: number;
  heartbeatFreshnessMs?: number;
  requestTimeoutMs?: number;
}

// BusError is a failure the bus itself replied with (Response.error): the
// request reached the bus and was definitively answered. Its presence (vs. a
// transport error) distinguishes a bus-side refusal from a never-answered call —
// the resume path and the heartbeat graceful-degrade key on it. Mirrors Go's
// busError.
export class BusError extends Error {
  constructor(
    readonly op: string,
    readonly busMessage: string,
  ) {
    super(`sextant: ${op}: ${busMessage}`);
    this.name = "BusError";
  }
}

// Identity is the client id (a bus-minted ULID) and display name read from the
// credential's JWT — authoritative, since it is the same JWT the bus
// authenticates.
export interface Identity {
  id: string;
  displayName: string;
}

// DISPLAY_NAME_TAG is the JWT tag prefix carrying the display name, hex-encoded
// (NATS lowercases raw tags, so the value is hex to survive). Mirrors
// wireapi.DisplayNameTag.
const DISPLAY_NAME_TAG = "display_name:";

// decodeDisplayNameTag returns the display name carried by tag, if it is one.
// Mirrors wireapi.DecodeDisplayNameTag. Pure-JS (no Buffer) so it runs in the
// browser entry too (ADR-0044): hexToBytes + TextDecoder. A malformed hex tag
// yields undefined rather than throwing (the tag is informational).
function decodeDisplayNameTag(tag: string): string | undefined {
  if (!tag.startsWith(DISPLAY_NAME_TAG)) return undefined;
  const hex = tag.slice(DISPLAY_NAME_TAG.length);
  if (hex.length % 2 !== 0) return undefined;
  let bytes: Uint8Array;
  try {
    bytes = hexToBytes(hex);
  } catch {
    return undefined;
  }
  return new TextDecoder("utf-8").decode(bytes);
}

// base64urlToBytes decodes a base64url segment (the JWT payload encoding) to
// bytes — pure-JS so it runs in the browser entry (ADR-0044): map base64url to
// standard base64, then atob to a binary string, then to bytes. atob is present
// in browsers and in Node ≥16. This is on the AUTH path (it reads the JWT the bus
// will authenticate), so it is unit-pinned against a known credential to prove it
// is byte-equivalent to the Buffer path it replaced.
function base64urlToBytes(seg: string): Uint8Array {
  let b64 = seg.replace(/-/g, "+").replace(/_/g, "/");
  // base64url drops '=' padding; atob wants the length padded to a multiple of 4.
  while (b64.length % 4 !== 0) b64 += "=";
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return bytes;
}

// identityFromCreds reads the client id (the JWT `name` claim = a bus-minted
// ULID) and display name (a `display_name:<hex>` tag) out of the decorated
// credential text. The JWT is base64url; we decode its payload segment without a
// crypto-verification step (the bus, not the client, verifies the signature —
// the client only reads what it will then authenticate as). Mirrors
// identityFromCreds in client.go.
export function identityFromCreds(credsText: string): Identity {
  const m = credsText.match(/-----BEGIN NATS USER JWT-----\s*([^\s-]+)/);
  if (!m) {
    throw new Error("sextant: credentials carry no NATS user JWT");
  }
  const segments = m[1]!.split(".");
  if (segments.length < 2) {
    throw new Error("sextant: malformed JWT in credentials");
  }
  let claims: { name?: string; nats?: { tags?: string[] } };
  try {
    claims = JSON.parse(new TextDecoder("utf-8").decode(base64urlToBytes(segments[1]!)));
  } catch (e) {
    throw new Error(`sextant: decode JWT payload: ${(e as Error).message}`);
  }
  const id = claims.name ?? "";
  if (id === "") {
    throw new Error("sextant: credentials carry no client id (JWT name claim)");
  }
  let displayName = "";
  for (const tag of claims.nats?.tags ?? []) {
    const decoded = decodeDisplayNameTag(tag);
    if (decoded !== undefined) {
      displayName = decoded;
      break;
    }
  }
  return { id, displayName };
}

// dialConnectOptions is the connection options object the two dialers share
// (ADR-0044): the per-client custom inbox (so call replies land where the
// credential's allow-list permits) and infinite reconnect (connection-loss is not
// exit — the SDK reconnects). The transport-specific bits — the authenticator
// (each transport ships its own credsAuthenticator) and the `connect` call itself
// — are supplied by the Node/browser dialer; this returns everything else so the
// two dialers differ only in their import source. It is plain data (no node:*),
// so it lives in the shared module.
export function dialConnectOptions(url: string, id: string): {
  servers: string[];
  name: string;
  inboxPrefix: string;
  maxReconnectAttempts: number;
  waitOnFirstConnect: boolean;
} {
  return {
    servers: [url],
    name: id,
    inboxPrefix: inboxPrefix(id),
    maxReconnectAttempts: -1, // reconnect forever; connection-loss != exit
    waitOnFirstConnect: false,
  };
}

// call invokes a Wire API operation: it sends canonical(input) as a request to
// sx.api.<id>.<op> and decodes the reply. The reply is Response{error?,result?};
// a non-empty error rejects with a BusError, otherwise the result JSON is
// returned (or undefined when the op has no output). Mirrors callConn in call.go.
//
// The input is serialized with the SAME canonical rule the codec uses, so the
// snake_case wire keys are emitted deterministically. The reply is parsed with
// the big-integer-preserving parser so a uint64 field (seq, revision) survives
// when it exceeds 2^53-1 (rare, but faithful).
export async function call(
  nc: NatsConnection,
  id: string,
  op: string,
  input: JSONValue,
  timeoutMs: number,
): Promise<JSONValue | undefined> {
  let reply: Msg;
  try {
    reply = await nc.request(callSubject(id, op), new TextEncoder().encode(canonical(input)), {
      timeout: timeoutMs,
    });
  } catch (e) {
    throw new Error(`sextant: ${op}: ${(e as Error).message}`);
  }
  const resp = parseJSON(new TextDecoder().decode(reply.data));
  if (resp === null || typeof resp !== "object" || Array.isArray(resp)) {
    throw new Error(`sextant: ${op}: malformed reply`);
  }
  const r = resp as { error?: JSONValue; result?: JSONValue };
  if (typeof r.error === "string" && r.error !== "") {
    throw new BusError(op, r.error);
  }
  return r.result;
}

// isUnknownOperation reports whether err is the bus's definitive "this operation
// does not exist" reply — the graceful-degrade signal for a bus that predates an
// op (e.g. clients.heartbeat). It keys on a BusError (the bus answered) whose
// message names the unknown-operation refusal, never on a transport error.
// Mirrors isUnknownOperation in heartbeat.go.
export function isUnknownOperation(err: unknown): boolean {
  return err instanceof BusError && err.busMessage.includes("unknown operation");
}

// ---------------------------------------------------------------------------
// ULID generation (for client-generated sub-ids)
// ---------------------------------------------------------------------------

const CROCKFORD = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";

// newULID generates a Crockford-base32 ULID string: 48-bit millisecond timestamp
// + 80 bits of randomness. Used for the client-generated sub-ids that name a
// subscription's delivery subject — unique per subscription, like Go's
// ulid.Make(). It is not the frame id (the bus mints those); it only needs to be
// a unique, well-formed ULID token.
export function newULID(): string {
  const time = Date.now();
  const rand = new Uint8Array(10);
  crypto.getRandomValues(rand); // Web Crypto: present in browsers and Node ≥15

  // Encode the 48-bit time into the first 10 base32 chars (the ULID spec packs
  // the 48-bit time in the high 10 characters of the 26-char string).
  const chars = new Array<string>(26);
  let t = time;
  for (let i = 9; i >= 0; i--) {
    chars[i] = CROCKFORD[t % 32]!;
    t = Math.floor(t / 32);
  }
  // Encode the 80 random bits into the last 16 base32 chars. Walk the 10 random
  // bytes as a bit stream, 5 bits per character.
  let bitBuffer = 0;
  let bitCount = 0;
  let out = 10;
  for (let i = 0; i < 10; i++) {
    bitBuffer = (bitBuffer << 8) | rand[i]!;
    bitCount += 8;
    while (bitCount >= 5) {
      bitCount -= 5;
      chars[out++] = CROCKFORD[(bitBuffer >> bitCount) & 0x1f]!;
    }
  }
  return chars.join("");
}
