/**
 * Publish implementation. Mirrors pkg/client/publish.go.
 *
 * Envelope is validated before publish so a missing trace_id or
 * invalid kind fails on the publisher, not on every downstream
 * consumer. The publish goes via core NATS (not JetStream) — events
 * are persisted by the JetStream stream subscription on the subject.
 */

import type { Client } from "./client.js";
import { encodeEnvelope, validateEnvelope, formatTimestamp } from "./envelope.js";
import { PROTO_VERSION } from "./proto_version.js";
import type { Envelope } from "./types.generated.js";

export async function publish(client: Client, subject: string, env: Envelope): Promise<void> {
  client.ensureOpen();
  if (!subject) throw new Error("client: publish requires a non-empty subject");

  if (!env.ts) env.ts = formatTimestamp();
  if (!env.proto_version) env.proto_version = PROTO_VERSION;

  validateEnvelope(env);
  const bytes = encodeEnvelope(env);
  client.nc.publish(subject, bytes);
  // Flush so the publish is on the wire before we return — matches
  // the spec's request/reply ordering expectations for the RPC path.
  await client.nc.flush();
}
