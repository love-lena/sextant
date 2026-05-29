/**
 * Envelope construction + validation helpers.
 *
 * Mirrors pkg/sextantproto/envelope.go: NewEnvelope (root span),
 * Child (causally-linked span), Validate (structural invariants).
 * Wire format is JSON; see specs/protocols/envelope-schema.md.
 */

import { randomUUID } from "node:crypto";

import type { Address, Envelope } from "./types.generated.js";

/** Envelope protocol version emitted by this client (matches Go ProtoVersion). */
export const PROTO_VERSION = "0.5.0";

/** Recognized envelope kinds. Mirrors sextantproto.Kind. */
export const KIND_AGENT_FRAME = "agent_frame";
export const KIND_LIFECYCLE = "lifecycle";
export const KIND_AUDIT = "audit";
export const KIND_TELEMETRY_SPAN = "telemetry_span";
export const KIND_TELEMETRY_METRIC = "telemetry_metric";
export const KIND_TELEMETRY_LOG = "telemetry_log";
export const KIND_USER_INPUT_REQUEST = "user_input_request";
export const KIND_USER_INPUT_RESPONSE = "user_input_response";
export const KIND_RPC_REQUEST = "rpc_request";
export const KIND_RPC_RESPONSE = "rpc_response";
export const KIND_HEARTBEAT = "heartbeat";

/** Recognized Address kinds. Mirrors sextantproto.AddressKind. */
export const ADDRESS_AGENT = "agent";
export const ADDRESS_OPERATOR = "operator";
export const ADDRESS_DAEMON = "daemon";
export const ADDRESS_UI = "ui";
export const ADDRESS_EXTERNAL = "external";

const VALID_ADDRESS_KINDS = new Set([
  ADDRESS_AGENT,
  ADDRESS_OPERATOR,
  ADDRESS_DAEMON,
  ADDRESS_UI,
  ADDRESS_EXTERNAL,
]);

/**
 * Format a Date (or "now") as the wire timestamp form: RFC 3339 with
 * exactly 6 fractional digits and a `Z` suffix. Mirrors
 * sextantproto.Timestamp.MarshalJSON.
 */
export function formatTimestamp(d: Date = new Date()): string {
  const iso = d.toISOString(); // e.g. "2026-05-24T12:34:56.789Z"
  // toISOString gives 3-digit fractional seconds. Pad to 6 to match Go.
  const dot = iso.indexOf(".");
  if (dot < 0) {
    return iso.replace("Z", ".000000Z");
  }
  const baseEnd = iso.indexOf("Z");
  const frac = iso.slice(dot + 1, baseEnd);
  const padded = (frac + "000000").slice(0, 6);
  return `${iso.slice(0, dot)}.${padded}Z`;
}

/** Parse a wire timestamp into a JS Date (microsecond precision lost — JS is ms). */
export function parseTimestamp(s: string): Date {
  return new Date(s);
}

/**
 * Build a root envelope (no causal parent). TraceID is set to the
 * envelope ID, SpanID is fresh — matches pkg/sextantproto.NewEnvelope.
 */
export function newEnvelope(
  kind: string,
  from: Address,
  payload: unknown,
): Envelope {
  const id = randomUUID();
  return {
    id,
    ts: formatTimestamp(),
    proto_version: PROTO_VERSION,
    from,
    trace_id: id,
    span_id: randomUUID(),
    kind,
    payload,
  };
}

/**
 * Build a child envelope under `parent`, preserving trace_id and
 * referencing parent.span_id as parent_span_id. ID, span_id, ts are
 * fresh. Mirrors `(Envelope).Child` in Go.
 */
export function childEnvelope(
  parent: Envelope,
  kind: string,
  from: Address,
  payload: unknown,
): Envelope {
  return {
    id: randomUUID(),
    ts: formatTimestamp(),
    proto_version: PROTO_VERSION,
    from,
    trace_id: parent.trace_id,
    span_id: randomUUID(),
    parent_span_id: parent.span_id,
    kind,
    payload,
  };
}

/**
 * Throw if envelope violates the structural invariants required by
 * specs/protocols/envelope-schema.md. Mirrors `(Envelope).Validate`.
 *
 * Called by `publish` so a malformed envelope fails on the publisher,
 * not on every downstream consumer.
 */
export function validateEnvelope(env: Envelope): void {
  if (!env.id) throw new Error("envelope: id is empty");
  if (!env.trace_id) throw new Error("envelope: trace_id is empty (required on every envelope)");
  if (!env.span_id) throw new Error("envelope: span_id is empty (required on every envelope)");
  if (!env.proto_version) throw new Error("envelope: proto_version is empty");
  if (!env.kind) throw new Error("envelope: kind is empty");
  if (!env.from || !VALID_ADDRESS_KINDS.has(env.from.kind)) {
    throw new Error(`envelope: from.kind ${JSON.stringify(env.from?.kind)} is not a recognized AddressKind`);
  }
  if (!env.ts) throw new Error("envelope: ts is empty");
}

/**
 * Parse and validate a JSON-encoded envelope from a NATS message
 * payload. Returns the envelope on success; throws on decode or
 * validation failure.
 *
 * Subscribers use this; on failure the message is Term'd at the
 * JetStream level so a malformed event is reported once.
 */
export function decodeEnvelope(data: Uint8Array): Envelope {
  const text = new TextDecoder().decode(data);
  let env: Envelope;
  try {
    env = JSON.parse(text) as Envelope;
  } catch (err) {
    throw new Error(
      `unmarshal envelope: ${err instanceof Error ? err.message : String(err)}`,
    );
  }
  validateEnvelope(env);
  return env;
}

/**
 * Encode an envelope to a UTF-8 byte array suitable for nc.publish.
 *
 * If `env.ts` is empty, it's set to `formatTimestamp()` first. Same
 * for `proto_version`. Matches Publish's small-fix behaviour in Go.
 */
export function encodeEnvelope(env: Envelope): Uint8Array {
  if (!env.ts) env.ts = formatTimestamp();
  if (!env.proto_version) env.proto_version = PROTO_VERSION;
  return new TextEncoder().encode(JSON.stringify(env));
}
