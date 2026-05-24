/**
 * Integration test for @sextant/client.
 *
 * Spawns nats-server in a temp dir, bootstraps the streams + KV buckets,
 * and exercises every public surface: connect, subscribe + publish,
 * RPC against a synthetic responder, KV put/get/watch.
 */

import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { randomUUID } from "node:crypto";

import {
  ADDRESS_OPERATOR,
  Client,
  KIND_AGENT_FRAME,
  KIND_RPC_RESPONSE,
  KVKeyNotFoundError,
  RPCError,
  RPCTimeoutError,
  connectWithConfig,
  decodeEnvelope,
  newEnvelope,
} from "../src/index.js";
import type { ListAgentsRequest, ListAgentsResponse } from "../src/index.js";
import {
  bootstrapJetStream,
  connectAsOperator,
  startNATS,
  type HarnessHandle,
} from "./nats-harness.js";

let nats: HarnessHandle;
let client: Client;

beforeAll(async () => {
  nats = await startNATS();
  await bootstrapJetStream(nats);
  client = await connectWithConfig({
    nats: { url: nats.url },
    operator: { user: nats.operatorUser, password: nats.operatorPassword },
  });
}, 60_000);

afterAll(async () => {
  if (client) await client.close();
  if (nats) await nats.stop();
}, 60_000);

describe("config", () => {
  it("requires nats.url", async () => {
    await expect(
      connectWithConfig({ operator: { user: "x", password: "y" } } as never),
    ).rejects.toThrow(/nats.url is required/);
  });

  it("rejects both password and credsPath", async () => {
    await expect(
      connectWithConfig({
        nats: { url: "nats://127.0.0.1:1" },
        operator: { user: "u", password: "p", credsPath: "/tmp/x" },
      }),
    ).rejects.toThrow(/mutually exclusive/);
  });
});

describe("publish + subscribe", () => {
  it("round-trips an envelope through JetStream", async () => {
    const agentUUID = randomUUID();
    const subject = `agents.${agentUUID}.frames`;

    // Spin up the subscriber BEFORE the publish so the consumer is
    // pinned and ready. DeliverAll catches the publish even if there's
    // a tiny delivery race.
    const iter = client.subscribe(subject, { deliverAll: true });

    const env = newEnvelope(
      KIND_AGENT_FRAME,
      { kind: ADDRESS_OPERATOR, id: "test-operator" },
      { frame_kind: "assistant_text", body: { text: "hello from ts" } },
    );
    await client.publish(subject, env);

    const it_ = iter[Symbol.asyncIterator]();
    const r = await Promise.race([
      it_.next(),
      new Promise<never>((_, reject) =>
        setTimeout(() => reject(new Error("subscribe timeout")), 5_000),
      ),
    ]);
    expect(r.done).toBe(false);
    expect(r.value.err).toBeUndefined();
    expect(r.value.envelope).toBeDefined();
    expect(r.value.envelope?.id).toBe(env.id);
    expect(r.value.envelope?.kind).toBe(KIND_AGENT_FRAME);
    expect(r.value.subject).toBe(subject);
    expect(r.value.streamSeq).toBeGreaterThanOrEqual(1n);
    await r.value.ack();
    // Ack twice — second call should be a no-op, not throw.
    await r.value.ack();

    await it_.return?.();
  }, 30_000);

  it("surfaces malformed envelopes via Message.err", async () => {
    const agentUUID = randomUUID();
    const subject = `agents.${agentUUID}.lifecycle`;

    const iter = client.subscribe(subject, { deliverAll: true });

    // Publish raw garbage directly via the test-only NATS handle.
    const raw = new TextEncoder().encode("not json at all");
    client.nc.publish(subject, raw);
    await client.nc.flush();

    const it_ = iter[Symbol.asyncIterator]();
    const r = await Promise.race([
      it_.next(),
      new Promise<never>((_, reject) =>
        setTimeout(() => reject(new Error("subscribe(err) timeout")), 5_000),
      ),
    ]);
    expect(r.done).toBe(false);
    expect(r.value.err).toBeDefined();
    expect(r.value.envelope).toBeUndefined();
    expect(r.value.subject).toBe(subject);

    await it_.return?.();
  }, 30_000);
});

describe("rpc", () => {
  it("returns the typed response from a synthetic responder", async () => {
    // Stand up a fake list_agents responder on the daemon's subject
    // using a raw NATS connection. The client.rpc call should
    // serialize the request, the responder unwraps the envelope, and
    // we publish a real rpc_response envelope back.
    const raw = await connectAsOperator(nats);
    const verb = "list_agents";
    const sub = raw.subscribe(`sextant.rpc.${verb}`, { max: 1 });
    const responderDone = (async () => {
      for await (const m of sub) {
        const req = decodeEnvelope(m.data);
        const replyTo = req.reply_to;
        if (!replyTo) {
          throw new Error("test responder: missing reply_to");
        }
        const reply = {
          id: randomUUID(),
          ts: new Date().toISOString().replace("Z", "000Z"),
          proto_version: "1.0",
          from: { kind: "daemon", id: "daemon-test" },
          trace_id: req.trace_id,
          span_id: randomUUID(),
          parent_span_id: req.span_id,
          kind: KIND_RPC_RESPONSE,
          payload: {
            result: { agents: [] },
            _terminal: true,
          },
        };
        raw.publish(replyTo, new TextEncoder().encode(JSON.stringify(reply)));
        await raw.flush();
        return;
      }
    })();
    // Give the responder a tick to subscribe before client.rpc fires.
    await new Promise((r) => setTimeout(r, 50));

    const resp = await client.rpc<ListAgentsRequest, ListAgentsResponse>(verb, {});
    await responderDone;
    expect(resp.agents).toEqual([]);
    await raw.close();
  }, 30_000);

  it("surfaces server-side errors as RPCError", async () => {
    const raw = await connectAsOperator(nats);
    const verb = "list_agents";
    const sub = raw.subscribe(`sextant.rpc.${verb}`, { max: 1 });
    const responderDone = (async () => {
      for await (const m of sub) {
        const req = decodeEnvelope(m.data);
        const replyTo = req.reply_to!;
        const reply = {
          id: randomUUID(),
          ts: new Date().toISOString().replace("Z", "000Z"),
          proto_version: "1.0",
          from: { kind: "daemon", id: "daemon-test" },
          trace_id: req.trace_id,
          span_id: randomUUID(),
          parent_span_id: req.span_id,
          kind: KIND_RPC_RESPONSE,
          payload: {
            error: { code: "capability_denied", message: "no can do" },
            _terminal: true,
          },
        };
        raw.publish(replyTo, new TextEncoder().encode(JSON.stringify(reply)));
        await raw.flush();
        return;
      }
    })();
    await new Promise((r) => setTimeout(r, 50));

    try {
      await client.rpc(verb, {});
      throw new Error("expected RPCError");
    } catch (e) {
      expect(e).toBeInstanceOf(RPCError);
      expect((e as RPCError).code).toBe("capability_denied");
    } finally {
      await responderDone.catch(() => {});
      await raw.close();
    }
  }, 30_000);

  it("times out when the server never replies", async () => {
    const start = Date.now();
    await expect(
      client.rpc("nonexistent_verb_no_responder", {}, { timeoutMs: 500 }),
    ).rejects.toBeInstanceOf(RPCTimeoutError);
    const dur = Date.now() - start;
    // Should fail close to the timeout, not minutes later.
    expect(dur).toBeLessThan(5_000);
  }, 10_000);
});

describe("kv", () => {
  it("round-trips put -> get", async () => {
    const bucket = "ui_state";
    const key = `roundtrip-${randomUUID()}`;
    const value = new TextEncoder().encode("hello kv");
    await client.putKV(bucket, key, value);
    const got = await client.getKV(bucket, key);
    expect(new TextDecoder().decode(got)).toBe("hello kv");
  }, 30_000);

  it("throws KVKeyNotFoundError on a missing key", async () => {
    const bucket = "ui_state";
    await expect(client.getKV(bucket, `missing-${randomUUID()}`)).rejects.toBeInstanceOf(
      KVKeyNotFoundError,
    );
  }, 30_000);

  it("emits updates on watchKV", async () => {
    const bucket = "ui_state";
    const key = `watch-${randomUUID()}`;
    // Seed the key before watching so we exercise both the initial
    // value delivery and a subsequent live update.
    await client.putKV(bucket, key, new TextEncoder().encode("v1"));

    const iter = client.watchKV(bucket, key);
    const it_ = iter[Symbol.asyncIterator]();

    const first = await Promise.race([
      it_.next(),
      new Promise<never>((_, reject) =>
        setTimeout(() => reject(new Error("watch initial timeout")), 5_000),
      ),
    ]);
    expect(first.done).toBe(false);
    expect(first.value.bucket).toBe(bucket);
    expect(first.value.key).toBe(key);
    expect(new TextDecoder().decode(first.value.value)).toBe("v1");

    await client.putKV(bucket, key, new TextEncoder().encode("v2"));
    const second = await Promise.race([
      it_.next(),
      new Promise<never>((_, reject) =>
        setTimeout(() => reject(new Error("watch v2 timeout")), 5_000),
      ),
    ]);
    expect(second.done).toBe(false);
    expect(new TextDecoder().decode(second.value.value)).toBe("v2");

    await it_.return?.();
  }, 30_000);
});

describe("client lifecycle", () => {
  it("close is idempotent", async () => {
    const c = await connectWithConfig({
      nats: { url: nats.url },
      operator: { user: nats.operatorUser, password: nats.operatorPassword },
    });
    await c.close();
    await c.close(); // second call must not throw
    expect(c.closed).toBe(true);
  }, 30_000);

  it("methods after close throw ClientClosedError", async () => {
    const c = await connectWithConfig({
      nats: { url: nats.url },
      operator: { user: nats.operatorUser, password: nats.operatorPassword },
    });
    await c.close();
    await expect(c.getKV("ui_state", "anything")).rejects.toThrow(/closed/);
  }, 30_000);
});
