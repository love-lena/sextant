/**
 * RPC implementation. Mirrors pkg/client/rpc.go.
 *
 * Each call:
 *   1. Provisions an ephemeral reply subject (NATS inbox).
 *   2. Subscribes to it before publishing the request.
 *   3. Builds an `rpc_request` envelope with auto-generated
 *      idempotency_key (override via opts) and reply_to set.
 *   4. Publishes on `sextant.rpc.<verb>`.
 *   5. Awaits exactly one terminal reply with timeout.
 */

import { randomUUID } from "node:crypto";

import { createInbox, type Subscription } from "nats";

import type { Client } from "./client.js";
import { newEnvelope, encodeEnvelope } from "./envelope.js";
import {
  KIND_RPC_REQUEST,
  KIND_RPC_RESPONSE,
  ADDRESS_OPERATOR,
} from "./proto_version.js";
import { RPCError, RPCTimeoutError } from "./errors.js";
import type { Envelope, RPCResponse, Address } from "./types.generated.js";

/** Per-call RPC knobs. */
export interface RPCOptions {
  /** Override the auto-generated UUID idempotency key. */
  idempotencyKey?: string;
  /** Override the default 10s per-call timeout. Milliseconds. */
  timeoutMs?: number;
}

/** Default per-call timeout. Matches specs/protocols/rpc-catalog.md §"Timeouts". */
const DEFAULT_TIMEOUT_MS = 10_000;

/**
 * Call the named sextant verb and return the typed result. Throws
 * `RPCError` on a server-side structured error, `RPCTimeoutError` on
 * a client-side timeout, or a plain Error on transport / decode
 * failures. Type parameters are caller-supplied:
 *
 * ```ts
 * const resp = await client.rpc<ListAgentsRequest, ListAgentsResponse>(
 *   "list_agents", {},
 * );
 * ```
 */
export async function rpc<Req = unknown, Resp = unknown>(
  client: Client,
  verb: string,
  req: Req,
  opts: RPCOptions = {},
): Promise<Resp> {
  client.ensureOpen();
  if (!verb) throw new Error("client: rpc: verb is empty");
  const timeoutMs = opts.timeoutMs && opts.timeoutMs > 0 ? opts.timeoutMs : DEFAULT_TIMEOUT_MS;
  const idempotencyKey = opts.idempotencyKey ?? randomUUID();

  const subject = `sextant.rpc.${verb}`;
  const reply = createInbox();
  const sub: Subscription = client.nc.subscribe(reply, { max: 1 });

  const from: Address = {
    kind: ADDRESS_OPERATOR,
    id: client.config.operator.user,
  };
  const env = newEnvelope(KIND_RPC_REQUEST, from, req);
  env.reply_to = reply;
  env.idempotency_key = idempotencyKey;

  try {
    client.nc.publish(subject, encodeEnvelope(env));
    await client.nc.flush();

    // Race the first delivery against the deadline timer.
    const iter = sub[Symbol.asyncIterator]();
    let timer: NodeJS.Timeout | undefined;
    const timeoutPromise = new Promise<{ timeout: true }>((resolve) => {
      timer = setTimeout(() => resolve({ timeout: true }), timeoutMs);
    });
    const nextPromise = iter.next().then((r) => ({ result: r }));
    let raced: { timeout: true } | { result: IteratorResult<import("nats").Msg> };
    try {
      raced = await Promise.race([nextPromise, timeoutPromise]);
    } finally {
      if (timer) clearTimeout(timer);
    }
    if ("timeout" in raced) {
      throw new RPCTimeoutError(verb, timeoutMs);
    }
    if (raced.result.done || !raced.result.value) {
      throw new Error("client: rpc reply subscription closed before terminal");
    }
    return decodeReply<Resp>(raced.result.value.data);
  } finally {
    sub.unsubscribe();
  }
}

function decodeReply<Resp>(data: Uint8Array): Resp {
  const text = new TextDecoder().decode(data);
  let env: Envelope;
  try {
    env = JSON.parse(text) as Envelope;
  } catch (err) {
    throw new Error(
      `client: rpc decode envelope: ${err instanceof Error ? err.message : String(err)}`,
    );
  }
  if (env.kind !== KIND_RPC_RESPONSE) {
    throw new Error(
      `client: rpc reply kind = ${JSON.stringify(env.kind)}, want ${KIND_RPC_RESPONSE}`,
    );
  }
  const payload = env.payload as RPCResponse | undefined;
  if (!payload) {
    throw new Error("client: rpc reply payload is empty");
  }
  if (!payload._terminal) {
    // M7 ships no streaming verbs. Match the Go client's behaviour:
    // surface as protocol violation rather than block forever.
    throw new Error(
      "client: rpc non-terminal reply received (streaming not supported in M7)",
    );
  }
  if (payload.error) {
    throw new RPCError(
      payload.error.code,
      payload.error.message,
      payload.error.details as Record<string, unknown> | undefined,
    );
  }
  // result may be absent for void-result verbs.
  return (payload.result ?? null) as Resp;
}
