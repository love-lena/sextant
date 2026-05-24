/**
 * KV implementation. Mirrors pkg/client/kv.go.
 *
 * The KV bucket must already exist — sextant bootstraps every bucket
 * in pkg/natsboot. PutKV does NOT create on demand (would hide schema
 * errors).
 */

import type { KV, KvEntry } from "nats";

import type { Client, StopRegistration } from "./client.js";
import { KVKeyNotFoundError } from "./errors.js";

/** Operation that produced a KV update. */
export type KVOp = "put" | "delete" | "purge";

/**
 * One observed change on a KV key. Mirrors pkg/client.KVUpdate.
 *
 * `err` is set when the underlying NATS watcher emits a failure — in
 * that case the other fields are zero-valued sentinels (empty bucket,
 * empty key, revision 0). Callers should check `err` before reading.
 */
export interface KVUpdate {
  bucket: string;
  key: string;
  /** Empty on delete / purge / err. */
  value: Uint8Array;
  revision: bigint;
  op: KVOp;
  timestamp: Date;
  /** Non-undefined when the watcher itself errored. */
  err?: Error;
}

/**
 * Write `value` at `key` in `bucket`. Creates the entry if missing or
 * overwrites the existing value.
 */
export async function putKV(
  client: Client,
  bucket: string,
  key: string,
  value: Uint8Array,
): Promise<void> {
  client.ensureOpen();
  if (!bucket || !key) throw new Error("client: putKV requires bucket and key");
  const kv = await openBucket(client, bucket);
  await kv.put(key, value);
}

/**
 * Read the current value of `key` from `bucket`. Throws
 * `KVKeyNotFoundError` when the key is absent.
 */
export async function getKV(
  client: Client,
  bucket: string,
  key: string,
): Promise<Uint8Array> {
  client.ensureOpen();
  if (!bucket || !key) throw new Error("client: getKV requires bucket and key");
  let kv: KV;
  try {
    kv = await openBucket(client, bucket);
  } catch (err) {
    if (isNotFound(err)) throw new KVKeyNotFoundError(bucket, key);
    throw err;
  }
  const entry = await kv.get(key);
  if (entry === null || entry === undefined) {
    throw new KVKeyNotFoundError(bucket, key);
  }
  if (entry.operation === "DEL" || entry.operation === "PURGE") {
    throw new KVKeyNotFoundError(bucket, key);
  }
  return entry.value;
}

/**
 * Subscribe to changes on `key` in `bucket`. The iterator yields one
 * KVUpdate per change. On subscription, the current value (if any) is
 * delivered first.
 *
 * Watcher failures (bucket disappears, connection drops, etc.) arrive
 * as an `err`-bearing KVUpdate followed by the iterator closing.
 * Callers should check `update.err` before reading `value` / `op`.
 */
export function watchKV(client: Client, bucket: string, key: string): AsyncIterable<KVUpdate> {
  client.ensureOpen();
  if (!bucket || !key) throw new Error("client: watchKV requires bucket and key");
  return {
    [Symbol.asyncIterator]() {
      return makeWatcher(client, bucket, key);
    },
  };
}

function makeWatcher(
  client: Client,
  bucket: string,
  key: string,
): AsyncIterator<KVUpdate> {
  const queue: KVUpdate[] = [];
  let waiter: ((res: IteratorResult<KVUpdate>) => void) | null = null;
  let closed = false;
  let stopFn: (() => void) | null = null;
  let reg: StopRegistration | null = null;

  const stop = (): void => {
    if (closed) return;
    closed = true;
    if (stopFn) {
      try {
        stopFn();
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

  reg = client.register(stop);

  let started = false;
  const start = async (): Promise<void> => {
    started = true;
    let kv: KV;
    try {
      kv = await openBucket(client, bucket);
    } catch (err) {
      enqueueErr(err);
      stop();
      if (reg) client.deregister(reg);
      return;
    }
    let watcher: Awaited<ReturnType<KV["watch"]>>;
    try {
      watcher = await kv.watch({ key });
    } catch (err) {
      enqueueErr(err);
      stop();
      if (reg) client.deregister(reg);
      return;
    }
    stopFn = () => watcher.stop();
    (async () => {
      try {
        for await (const entry of watcher) {
          if (closed) break;
          // Boundary marker between current values and live updates is
          // not surfaced by nats.js as nulls — it's just the absence
          // of further entries up to the "all seen" status. We just
          // emit every entry the watcher yields.
          enqueueUpdate(entryToUpdate(entry));
        }
      } catch (err) {
        if (!closed) enqueueErr(err);
      } finally {
        stop();
        if (reg) client.deregister(reg);
      }
    })().catch(() => {
      /* enqueueErr above surfaces */
    });
  };

  const enqueueUpdate = (u: KVUpdate): void => {
    if (waiter) {
      const w = waiter;
      waiter = null;
      w({ value: u, done: false });
      return;
    }
    queue.push(u);
  };

  const enqueueErr = (err: unknown): void => {
    // Surface watcher errors as a real KVUpdate carrying `err`. Mirrors
    // subscribe.ts's Message.err shape; callers check `update.err`
    // before reading the value/op fields.
    const e = err instanceof Error ? err : new Error(String(err));
    enqueueUpdate({
      bucket,
      key,
      value: new Uint8Array(),
      revision: 0n,
      op: "put",
      timestamp: new Date(),
      err: e,
    });
  };

  return {
    async next(): Promise<IteratorResult<KVUpdate>> {
      if (!started) {
        start().catch(() => {
          /* enqueueErr handles surface */
        });
      }
      const item = queue.shift();
      if (item !== undefined) return { value: item, done: false };
      if (closed) return { value: undefined, done: true };
      return new Promise<IteratorResult<KVUpdate>>((resolve) => {
        waiter = resolve;
      });
    },
    async return(): Promise<IteratorResult<KVUpdate>> {
      stop();
      if (reg) client.deregister(reg);
      return { value: undefined, done: true };
    },
  };
}

async function openBucket(client: Client, bucket: string): Promise<KV> {
  const kvm = client.js.views.kv(bucket);
  // js.views.kv returns a Promise<KV> in nats.js 2.x.
  return kvm;
}

function entryToUpdate(entry: KvEntry): KVUpdate {
  let op: KVOp = "put";
  if (entry.operation === "DEL") op = "delete";
  else if (entry.operation === "PURGE") op = "purge";
  return {
    bucket: entry.bucket,
    key: entry.key,
    value: entry.value,
    revision: BigInt(entry.revision),
    op,
    timestamp: entry.created,
  };
}

function isNotFound(err: unknown): boolean {
  if (!err) return false;
  const msg = err instanceof Error ? err.message : String(err);
  return /bucket.*not.*found|stream not found|no.*bucket/i.test(msg);
}
