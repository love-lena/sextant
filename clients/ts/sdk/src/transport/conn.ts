// The connection layer: identity from the credential, URL resolution, the Wire
// API call envelope, and a sub-id generator. Mirrors the Go SDK's call.go +
// the Connect URL/identity resolution in client.go.

import { readFile } from "node:fs/promises";
import { randomFillSync } from "node:crypto";
import {
  connect as natsConnect,
  credsAuthenticator,
  type NatsConnection,
  type Msg,
} from "nats";
import { callSubject, inboxPrefix } from "./callsubjects.js";
import { canonical, parseJSON } from "../wire/codec.js";
import type { JSONValue } from "../types.js";

// ConnectOptions configures connect() (and connectIssuer()). All optional except
// credsPath. Mirrors Go's Options.
export interface ConnectOptions {
  credsPath: string; // path to the .creds file (required) — the client's own identity
  url?: string; // bus NATS URL; wins over connInfoPath
  connInfoPath?: string; // bus.json discovery file (fallback)
  skewToleranceMs?: number; // default 300_000 (5m)
  log?: (msg: string, ...a: unknown[]) => void; // default console.error
  heartbeatIntervalMs?: number; // default 15_000
  heartbeatFreshnessMs?: number; // default 45_000
  requestTimeoutMs?: number; // per-call request timeout; default 30_000
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
// Mirrors wireapi.DecodeDisplayNameTag.
function decodeDisplayNameTag(tag: string): string | undefined {
  if (!tag.startsWith(DISPLAY_NAME_TAG)) return undefined;
  const hex = tag.slice(DISPLAY_NAME_TAG.length);
  if (hex.length % 2 !== 0) return undefined;
  const buf = Buffer.from(hex, "hex");
  if (buf.length !== hex.length / 2) return undefined;
  return buf.toString("utf8");
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
    claims = JSON.parse(Buffer.from(segments[1]!, "base64url").toString("utf8"));
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

// resolveURL determines the bus URL: an explicit url wins, otherwise it reads
// the bus.json discovery file. The discovery file is re-read on demand so a bus
// that restarts on a new port is followed (the Go SDK re-resolves on every dial;
// the nats npm client does not expose a per-dial custom dialer the same way, so
// the SDK re-resolves on an explicit reconnect-on-failure path — see
// resolveURLNow). Returns the URL and the discovery path used (empty when the
// URL was pinned).
export async function resolveURL(opts: ConnectOptions): Promise<{ url: string; connInfoPath: string }> {
  if (opts.url) {
    return { url: opts.url, connInfoPath: "" };
  }
  if (opts.connInfoPath) {
    const url = await readBusURL(opts.connInfoPath);
    return { url, connInfoPath: opts.connInfoPath };
  }
  throw new Error("sextant: no bus URL (set url or connInfoPath)");
}

// readBusURL reads the url field from a bus.json discovery file. Mirrors
// conninfo.Read.
export async function readBusURL(connInfoPath: string): Promise<string> {
  const text = await readFile(connInfoPath, "utf8");
  let info: { url?: string };
  try {
    info = JSON.parse(text);
  } catch (e) {
    throw new Error(`sextant: parse ${connInfoPath}: ${(e as Error).message}`);
  }
  if (!info.url) {
    throw new Error(`sextant: ${connInfoPath} carries no url`);
  }
  return info.url;
}

// dialOptions builds the NATS connection options shared by Client and Issuer:
// creds auth, the per-client custom inbox (so call replies land where the
// credential's allow-list permits), and infinite reconnect (connection-loss is
// not exit — the SDK reconnects). The discovery file is re-read on a failed dial
// by reconnect (servers list is refreshed via the reconnect callback in conn).
export async function dialNats(
  url: string,
  credsText: string,
  id: string,
): Promise<NatsConnection> {
  return natsConnect({
    servers: [url],
    name: id,
    authenticator: credsAuthenticator(new TextEncoder().encode(credsText)),
    inboxPrefix: inboxPrefix(id),
    maxReconnectAttempts: -1, // reconnect forever; connection-loss != exit
    waitOnFirstConnect: false,
  });
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
  randomFillSync(rand);

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
