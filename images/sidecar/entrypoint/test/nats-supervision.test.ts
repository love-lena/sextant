/**
 * Unit tests for the sidecar's NATS-connection supervision module.
 *
 * Regresses slug:bug-sidecar-nats-disconnect-no-reconnect.
 *
 * The supervision module is the sidecar-side symmetric resilience to
 * the daemon-side `TestDaemonRestartsNATSAfterKill` proof: when the
 * underlying NATS server disappears (laptop sleep, daemon restart,
 * network blip) the sidecar must (a) keep retrying forever rather than
 * giving up, (b) log reconnect events at warn so operators can find
 * them in the journal, (c) re-arm any subscriptions that don't
 * auto-recover, and (d) exit non-zero on a permanent failure so the
 * daemon's supervisor spawns a fresh incarnation rather than leaving a
 * silently-deaf agent.
 *
 * These tests exercise the supervision module against a fake
 * NatsConnection (just the surface the module touches — `status()`
 * iterable and a `closed()` promise). They do not require a live
 * NATS server.
 */

import { describe, expect, it } from "vitest";
import {
  watchNATSStatus,
  watchNATSClosed,
  type StatusLike,
  type NATSConnLike,
} from "../src/nats-supervision.js";

/**
 * Build a fake NatsConnection-shaped object whose `status()` iterator
 * is driven by a queue we control from the test. `push(status)` enqueues
 * an event; `end()` ends the iterator (simulating the connection
 * closing). `closed()` returns a promise the test can resolve via
 * `closeWith(err?)`.
 */
function newFakeConn(): {
  conn: NATSConnLike;
  push: (s: StatusLike) => void;
  endStatus: () => void;
  closeWith: (err?: Error) => void;
} {
  type Resolver = (r: IteratorResult<StatusLike>) => void;
  const queue: IteratorResult<StatusLike>[] = [];
  let waiter: Resolver | null = null;
  let statusEnded = false;

  const enqueue = (r: IteratorResult<StatusLike>): void => {
    if (waiter) {
      const w = waiter;
      waiter = null;
      w(r);
      return;
    }
    queue.push(r);
  };

  const statusIter: AsyncIterableIterator<StatusLike> = {
    [Symbol.asyncIterator](): AsyncIterableIterator<StatusLike> {
      return statusIter;
    },
    async next(): Promise<IteratorResult<StatusLike>> {
      const next = queue.shift();
      if (next !== undefined) return next;
      if (statusEnded) return { value: undefined, done: true };
      return new Promise<IteratorResult<StatusLike>>((resolve) => {
        waiter = resolve;
      });
    },
    async return(): Promise<IteratorResult<StatusLike>> {
      // Mirror nats.js's status() iterator: a return() call ends the
      // iteration. Any pending next() must resolve with done:true so
      // the consumer loop doesn't hang on shutdown.
      statusEnded = true;
      if (waiter) {
        const w = waiter;
        waiter = null;
        w({ value: undefined, done: true });
      }
      return { value: undefined, done: true };
    },
  };

  let resolveClosed: (v: void | Error) => void = () => {};
  const closedPromise = new Promise<void | Error>((resolve) => {
    resolveClosed = resolve;
  });

  const conn: NATSConnLike = {
    status: () => statusIter,
    closed: () => closedPromise,
  };

  return {
    conn,
    push: (s: StatusLike) => enqueue({ value: s, done: false }),
    endStatus: () => {
      statusEnded = true;
      if (waiter) {
        const w = waiter;
        waiter = null;
        w({ value: undefined, done: true });
      }
    },
    closeWith: (err?: Error) => {
      resolveClosed(err ?? undefined);
    },
  };
}

interface CapturedLog {
  level: "info" | "warn" | "error";
  msg: string;
  extra?: Record<string, unknown>;
}

describe("watchNATSStatus", () => {
  it("logs disconnect events at warn (not info)", async () => {
    const fake = newFakeConn();
    const logs: CapturedLog[] = [];

    const stop = watchNATSStatus(fake.conn, {
      log: (level, msg, extra) => logs.push({ level, msg, extra }),
    });

    fake.push({ type: "disconnect", data: "nats://127.0.0.1:4222" });

    // Yield the event loop so the watcher's iterator step runs.
    await new Promise((resolve) => setTimeout(resolve, 5));

    fake.endStatus();
    await stop();

    const disconnectLog = logs.find((l) => l.msg.includes("nats disconnect"));
    expect(disconnectLog).toBeDefined();
    // Spec: ticket §"Fix shape" item 4 — reconnect events at warn so
    // operators investigating downtime can find them.
    expect(disconnectLog!.level).toBe("warn");
  });

  it("logs reconnect events at warn and invokes onReconnect", async () => {
    const fake = newFakeConn();
    const logs: CapturedLog[] = [];
    let reconnectCount = 0;

    const stop = watchNATSStatus(fake.conn, {
      log: (level, msg, extra) => logs.push({ level, msg, extra }),
      onReconnect: () => {
        reconnectCount += 1;
      },
    });

    fake.push({ type: "disconnect", data: "nats://127.0.0.1:4222" });
    fake.push({ type: "reconnect", data: "nats://127.0.0.1:4222" });

    await new Promise((resolve) => setTimeout(resolve, 10));

    fake.endStatus();
    await stop();

    const reconnectLog = logs.find((l) => l.msg.includes("nats reconnect"));
    expect(reconnectLog).toBeDefined();
    expect(reconnectLog!.level).toBe("warn");
    expect(reconnectCount).toBe(1);
  });

  it("treats successive disconnect/reconnect cycles as separate events", async () => {
    const fake = newFakeConn();
    const logs: CapturedLog[] = [];
    let reconnectCount = 0;

    const stop = watchNATSStatus(fake.conn, {
      log: (level, msg, extra) => logs.push({ level, msg, extra }),
      onReconnect: () => {
        reconnectCount += 1;
      },
    });

    fake.push({ type: "disconnect", data: "nats://127.0.0.1:4222" });
    fake.push({ type: "reconnect", data: "nats://127.0.0.1:4222" });
    fake.push({ type: "disconnect", data: "nats://127.0.0.1:4222" });
    fake.push({ type: "reconnect", data: "nats://127.0.0.1:4222" });

    await new Promise((resolve) => setTimeout(resolve, 15));

    fake.endStatus();
    await stop();

    expect(reconnectCount).toBe(2);
    // Two disconnect-warn entries.
    expect(logs.filter((l) => l.msg.includes("nats disconnect")).length).toBe(2);
    // Two reconnect-warn entries.
    expect(logs.filter((l) => l.msg.includes("nats reconnect")).length).toBe(2);
  });

  it("logs server errors at warn but does not exit", async () => {
    const fake = newFakeConn();
    const logs: CapturedLog[] = [];

    const stop = watchNATSStatus(fake.conn, {
      log: (level, msg, extra) => logs.push({ level, msg, extra }),
    });

    fake.push({ type: "error", data: "AUTHORIZATION_VIOLATION" });

    await new Promise((resolve) => setTimeout(resolve, 5));

    fake.endStatus();
    await stop();

    const errLog = logs.find((l) => l.msg.includes("nats async error"));
    expect(errLog).toBeDefined();
    // Single events are warnings — only a permanent close (handled by
    // watchNATSClosed) drives an exit.
    expect(errLog!.level).toBe("warn");
  });

  it("stops cleanly when stop() is called before the iterator ends", async () => {
    const fake = newFakeConn();
    const stop = watchNATSStatus(fake.conn, {
      log: () => {
        /* drop */
      },
    });
    // No events queued — stop() must resolve.
    await stop();
    expect(true).toBe(true);
  });
});

describe("watchNATSClosed", () => {
  it("invokes the exit callback when the connection closes with an error", async () => {
    const fake = newFakeConn();
    const calls: Array<Error | undefined> = [];

    watchNATSClosed(fake.conn, (err) => {
      calls.push(err);
    });

    fake.closeWith(new Error("permanent: bad credentials"));

    // Allow the closed() promise to resolve and the callback to fire.
    await new Promise((resolve) => setTimeout(resolve, 5));

    expect(calls).toHaveLength(1);
    expect(calls[0]).toBeInstanceOf(Error);
    expect(calls[0]!.message).toContain("permanent");
  });

  it("invokes the exit callback even when the close has no error", async () => {
    // The sidecar still wants to exit if the connection silently closes
    // without our shutdown handler having driven it — the daemon's
    // supervisor restarts the agent. The callback is responsible for
    // distinguishing "we initiated this close" from "the connection
    // died on us" via a flag in scope.
    const fake = newFakeConn();
    const calls: Array<Error | undefined> = [];

    watchNATSClosed(fake.conn, (err) => {
      calls.push(err);
    });

    fake.closeWith(undefined);

    await new Promise((resolve) => setTimeout(resolve, 5));

    expect(calls).toHaveLength(1);
    expect(calls[0]).toBeUndefined();
  });
});
