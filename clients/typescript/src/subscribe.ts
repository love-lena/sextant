/**
 * Subscribe implementation.
 *
 * Builds an ephemeral JetStream pull consumer, decodes each delivery
 * into a typed Envelope, exposes the result as an AsyncIterable<Message>.
 * Mirrors pkg/client/subscribe.go: same default-new delivery, same
 * fromSeq / deliverAll surfaces, same malformed-envelope handling
 * (Term the message, surface as `Message { err }`).
 *
 * Why ephemeral pull rather than the OrderedConsumer wrapper: the
 * nats.js 2.x OrderedPullConsumerImpl doesn't reliably start
 * delivering messages on first `consume()` against nats-server 2.14 in
 * our integration setup — the underlying consumer-create flow hangs
 * 30s before erroring. An ephemeral pull consumer (with a fresh,
 * unique durable name) provides the same "from this point forward,
 * delivered in stream order, no other subscribers" semantics for a
 * single client, which is what the subscribe contract needs.
 */

import {
  AckPolicy,
  DeliverPolicy,
  type Consumer,
  type ConsumerInfo,
  type JsMsg,
  type ConsumerConfig,
} from "nats";
import { randomUUID } from "node:crypto";

import type { Client } from "./client.js";
import { decodeEnvelope } from "./envelope.js";
import type { Envelope } from "./types.generated.js";

export interface SubscribeOptions {
  /** Start delivery at this JetStream stream sequence. */
  fromSeq?: bigint;
  /** Replay every message currently in the stream(s) before going live. */
  deliverAll?: boolean;
}

/**
 * One received envelope. `err` is set when the wire bytes could not be
 * decoded — in that case `envelope` is undefined but `subject` and
 * `streamSeq` are populated from the raw JetStream message so callers
 * can correlate / resume.
 */
export interface Message {
  envelope?: Envelope;
  subject: string;
  streamSeq: bigint;
  consumerSeq: bigint;
  timestamp: Date;
  /** Ack to JetStream. Safe to call multiple times — fires exactly once. */
  ack(): Promise<void>;
  /** Non-undefined when envelope decode failed. */
  err?: Error;
}

/**
 * Subscribe to a subject pattern. Default delivery is "new" — only
 * messages published after the consumer is built are delivered.
 *
 * Returns an AsyncIterable that closes when the Client is closed or
 * the iterator is broken out of.
 */
export function subscribe(
  client: Client,
  subject: string,
  opts: SubscribeOptions = {},
): AsyncIterable<Message> {
  client.ensureOpen();
  if (!subject) throw new Error("client: subscribe requires a non-empty subject");
  return {
    [Symbol.asyncIterator]() {
      return makeAsyncIterator(client, subject, opts);
    },
  };
}

/** Alias for `subscribe(c, subject, { fromSeq })`. Mirrors Go SubscribeFromSeq. */
export function subscribeFromSeq(
  client: Client,
  subject: string,
  fromSeq: bigint,
): AsyncIterable<Message> {
  return subscribe(client, subject, { fromSeq });
}

function makeAsyncIterator(
  client: Client,
  subject: string,
  opts: SubscribeOptions,
): AsyncIterator<Message> {
  // Bounded buffer + waiter pair forms a single-consumer queue.
  const queue: Message[] = [];
  let waiter: ((res: IteratorResult<Message>) => void) | null = null;
  let closed = false;
  let started = false;
  let cleanup: (() => Promise<void>) | null = null;

  const stop = (): void => {
    if (closed) return;
    closed = true;
    if (cleanup) {
      cleanup().catch(() => {
        /* ignore */
      });
    }
    if (waiter) {
      const w = waiter;
      waiter = null;
      w({ value: undefined, done: true });
    }
  };

  const reg = client.register(stop);

  const start = async (): Promise<void> => {
    started = true;
    let streamName: string;
    let ci: ConsumerInfo;
    let consumer: Consumer;
    try {
      streamName = await resolveStream(client, subject);
      const config: Partial<ConsumerConfig> = {
        name: `ts_client_${randomUUID().replace(/-/g, "")}`,
        ack_policy: AckPolicy.Explicit,
        filter_subjects: [subject],
        deliver_policy: DeliverPolicy.New,
      };
      if (opts.fromSeq !== undefined && opts.fromSeq > 0n) {
        config.deliver_policy = DeliverPolicy.StartSequence;
        config.opt_start_seq = Number(opts.fromSeq);
      } else if (opts.deliverAll) {
        config.deliver_policy = DeliverPolicy.All;
      }
      ci = await client.jsm.consumers.add(streamName, config);
      // Install a delete-on-close shim immediately. If stop() fires
      // between here and the full cleanup below, the consumer still
      // gets torn down. If stop() already fired during the await of
      // consumers.add, run the cleanup synchronously now and bail.
      const ciName = ci.name;
      const deleteConsumer = async (): Promise<void> => {
        try {
          await client.jsm.consumers.delete(streamName, ciName);
        } catch {
          /* ignore */
        }
      };
      cleanup = deleteConsumer;
      if (closed) {
        await deleteConsumer();
        client.deregister(reg);
        return;
      }
      consumer = await client.js.consumers.get(streamName, ciName);
    } catch (err) {
      enqueueErr(err instanceof Error ? err : new Error(String(err)), subject);
      stop();
      client.deregister(reg);
      return;
    }

    let consumeMessages: Awaited<ReturnType<Consumer["consume"]>>;
    try {
      consumeMessages = await consumer.consume();
    } catch (err) {
      enqueueErr(err instanceof Error ? err : new Error(String(err)), subject);
      stop();
      client.deregister(reg);
      await client.jsm.consumers.delete(streamName, ci.name).catch(() => {});
      return;
    }
    cleanup = async () => {
      try {
        consumeMessages.stop();
      } catch {
        /* ignore */
      }
      try {
        await client.jsm.consumers.delete(streamName, ci.name);
      } catch {
        /* ignore */
      }
    };
    if (closed) {
      // stop() fired while consume() was resolving. Run the full
      // cleanup we just installed (stop the consume + delete the
      // consumer) and exit without yielding any messages.
      const c = cleanup;
      cleanup = null;
      await c();
      client.deregister(reg);
      return;
    }

    (async () => {
      try {
        for await (const m of consumeMessages) {
          if (closed) break;
          handleMessage(m);
        }
      } catch (err) {
        if (!closed) {
          enqueueErr(
            err instanceof Error ? err : new Error(String(err)),
            subject,
          );
        }
      } finally {
        stop();
        client.deregister(reg);
      }
    })().catch(() => {
      /* enqueueErr already routes errors */
    });
  };

  const handleMessage = (m: JsMsg): void => {
    const info = m.info;
    const streamSeq = BigInt(info.streamSequence);
    const consumerSeq = BigInt(info.deliverySequence);
    // timestampNanos is a number in nats.js 2.x; divide for ms.
    const timestamp = new Date(Math.floor(info.timestampNanos / 1_000_000));
    let envelope: Envelope;
    try {
      envelope = decodeEnvelope(m.data);
    } catch (err) {
      // Term so JetStream doesn't redeliver garbage.
      try {
        m.term();
      } catch {
        /* ignore */
      }
      enqueueMsg({
        subject: m.subject,
        streamSeq,
        consumerSeq,
        timestamp,
        err: err instanceof Error ? err : new Error(String(err)),
        ack: async () => {
          /* no-op */
        },
      });
      return;
    }
    let acked = false;
    enqueueMsg({
      envelope,
      subject: m.subject,
      streamSeq,
      consumerSeq,
      timestamp,
      ack: async () => {
        if (acked) return;
        acked = true;
        m.ack();
      },
    });
  };

  const enqueueMsg = (msg: Message): void => {
    if (waiter) {
      const w = waiter;
      waiter = null;
      w({ value: msg, done: false });
      return;
    }
    queue.push(msg);
  };

  const enqueueErr = (err: Error, subj: string): void => {
    enqueueMsg({
      subject: subj,
      streamSeq: 0n,
      consumerSeq: 0n,
      timestamp: new Date(),
      err,
      ack: async () => {
        /* no-op */
      },
    });
  };

  return {
    async next(): Promise<IteratorResult<Message>> {
      if (!started) {
        start().catch(() => {
          /* enqueueErr handles surface */
        });
      }
      const item = queue.shift();
      if (item !== undefined) return { value: item, done: false };
      if (closed) return { value: undefined, done: true };
      return new Promise<IteratorResult<Message>>((resolve) => {
        waiter = resolve;
      });
    },
    async return(): Promise<IteratorResult<Message>> {
      stop();
      client.deregister(reg);
      return { value: undefined, done: true };
    },
  };
}

/**
 * Resolve the JetStream stream a subject belongs to. The server is the
 * authority — we don't try to mirror the M2 stream layout here.
 */
async function resolveStream(client: Client, subject: string): Promise<string> {
  try {
    return await client.jsm.streams.find(subject);
  } catch (err) {
    throw new Error(
      `client: resolve stream for subject ${JSON.stringify(subject)}: ${
        err instanceof Error ? err.message : String(err)
      }`,
    );
  }
}
