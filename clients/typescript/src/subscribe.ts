/**
 * Subscribe implementation.
 *
 * Builds a JetStream ordered consumer, decodes each delivery into a
 * typed Envelope, exposes the result as an AsyncIterable<Message>.
 * Mirrors pkg/client/subscribe.go: same default-new delivery, same
 * WithStartSeq / WithDeliverAll surfaces, same malformed-envelope
 * handling (Term the message, surface as `Message { err }`).
 */

import { DeliverPolicy, type Consumer, type JsMsg, type OrderedConsumerOptions } from "nats";

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
  let consumeAbort: (() => void) | null = null;

  const stop = (): void => {
    if (closed) return;
    closed = true;
    if (consumeAbort) {
      try {
        consumeAbort();
      } catch {
        /* ignore */
      }
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
    let consumer: Consumer;
    try {
      const streamName = await resolveStream(client, subject);
      const consumerOpts: Partial<OrderedConsumerOptions> = {
        filterSubjects: [subject],
      };
      if (opts.fromSeq !== undefined && opts.fromSeq > 0n) {
        consumerOpts.opt_start_seq = Number(opts.fromSeq);
        consumerOpts.deliver_policy = DeliverPolicy.StartSequence;
      } else if (opts.deliverAll) {
        consumerOpts.deliver_policy = DeliverPolicy.All;
      } else {
        consumerOpts.deliver_policy = DeliverPolicy.New;
      }
      consumer = await client.js.consumers.get(streamName, consumerOpts);
    } catch (err) {
      // Surface a single error then close the iterator.
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
      return;
    }
    consumeAbort = () => consumeMessages.stop();

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
      // The body already routes errors into the queue; this catch is
      // just to make the unawaited promise lint-clean.
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
        // Kick the background setup on first call. Failures will be
        // enqueued via enqueueErr so callers see them on this same
        // iterator.
        start().catch(() => {
          /* enqueueErr handles surface; here just keeps the promise rejection silent */
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
  const stream = await client.jsm.streams.find(subject);
  if (!stream) {
    throw new Error(`client: resolve stream for subject ${JSON.stringify(subject)}: not found`);
  }
  return stream;
}
