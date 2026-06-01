/**
 * NATS-connection supervision.
 *
 * Regresses slug:bug-sidecar-nats-disconnect-no-reconnect.
 *
 * The base @sextant/client already pins `maxReconnectAttempts: -1` plus
 * the spec-bounded jitter (see `clients/typescript/src/client.ts`
 * `Client.buildNATSOptions`), so the underlying nats.js client will
 * retry forever on its own. What was missing is the sidecar-side
 * observation of those events — without watching `nc.status()` and
 * `nc.closed()` the sidecar can't:
 *
 *   1. surface disconnect/reconnect to operators (the ticket's
 *      `heartbeat publish failed: DISCONNECT` smoking-gun line lived in
 *      the journal but said nothing about whether reconnect ever
 *      happened, because nobody was listening for it);
 *
 *   2. re-arm subscriptions that didn't auto-recover (e.g. the inbox
 *      JetStream pull consumer may be lost across a server restart and
 *      need explicit resubscription on the `reconnect` event);
 *
 *   3. exit non-zero on a *permanent* failure so the daemon's
 *      supervisor spawns a fresh incarnation rather than the sidecar
 *      hanging silently.
 *
 * This module is the structural piece that does the observation. The
 * `index.ts` wiring layers the policy (re-arm inbox loop on reconnect,
 * process.exit(1) on closed) on top.
 *
 * The module is structured around a minimal `NATSConnLike` interface
 * (just `status()` + `closed()`) so unit tests can drive synthetic
 * events without spinning up a NATS server.
 */

/** One event from the NATS status iterator. Subset of nats.Status. */
export interface StatusLike {
  /**
   * Event type. The values we care about are the strings produced by
   * `nats.Events` / `nats.DebugEvents`: `"disconnect"`, `"reconnect"`,
   * `"update"`, `"ldm"`, `"error"`, `"reconnecting"`, `"staleConnection"`.
   * We accept a plain string here so tests can drive arbitrary types
   * and the nats.js enum doesn't have to leak through this module's API.
   */
  type: string;
  /** Implementation-defined extra context. Server URL, error code, etc. */
  data: unknown;
}

/**
 * Minimal NatsConnection surface this module touches. Mirrors the
 * subset of nats.NatsConnection that's actually used so tests can
 * supply a fake without implementing the full API.
 */
export interface NATSConnLike {
  status(): AsyncIterable<StatusLike>;
  /**
   * Resolves when the connection closes. The underlying nats.js
   * promise resolves with either `void` (clean close, including ones
   * we initiated ourselves) or an `Error` (permanent failure — e.g.
   * authentication rejected, max-reconnect-attempts hit). Maps to
   * `nats.NatsConnection.closed()`.
   */
  closed(): Promise<void | Error>;
}

/** Log shim — matches the index.ts log() function's three-arg shape. */
export type LogFn = (
  level: "info" | "warn" | "error",
  msg: string,
  extra?: Record<string, unknown>,
) => void;

/** Callbacks the supervisor invokes on transitions. */
export interface NATSSupervisorCallbacks {
  log: LogFn;
  /**
   * Invoked once per `reconnect` event from the status iterator. The
   * sidecar wires this to re-arm the inbox subscription so any
   * JetStream consumer lost across the server restart is rebuilt.
   *
   * Errors thrown by the callback are caught + logged at `error` so a
   * misbehaving handler can't crash the supervisor loop.
   */
  onReconnect?: () => void | Promise<void>;
  /**
   * Invoked once per `disconnect` event. Optional — most callers only
   * care about the symmetric `reconnect` signal — but exposed so a
   * caller can pause work in flight if needed.
   */
  onDisconnect?: () => void | Promise<void>;
}

/**
 * Watch the NATS status stream for the lifetime of the connection.
 *
 * Returns a `stop()` function the caller invokes during graceful
 * shutdown to break out of the iterator. The returned promise from
 * `stop()` resolves when the watcher loop has exited (so the caller
 * can sequence cleanup).
 *
 * Spec: ticket §"Fix shape" item 4 — disconnect/reconnect log at
 * `warn`, not `info`. The previous behaviour was no log at all (no
 * one watched), which made the failure mode impossible to diagnose
 * from the journal.
 */
export function watchNATSStatus(
  nc: NATSConnLike,
  cb: NATSSupervisorCallbacks,
): () => Promise<void> {
  let stopped = false;
  let iter: AsyncIterator<StatusLike> | null = null;

  const loop = async (): Promise<void> => {
    const iterable = nc.status();
    iter = iterable[Symbol.asyncIterator]();
    try {
      while (!stopped) {
        const { value, done } = await iter.next();
        if (done) return;
        if (stopped) return;
        await dispatch(value, cb);
      }
    } catch (err) {
      // The iterator throwing typically means the NATS client torn
      // itself down. The `watchNATSClosed` companion will pick up
      // the permanent-failure signal; here we just exit the loop
      // quietly so we don't double-log.
      const message = err instanceof Error ? err.message : String(err);
      if (!stopped) {
        cb.log("warn", "nats status iterator ended with error", {
          err: message,
        });
      }
    }
  };

  const running = loop();

  return async () => {
    stopped = true;
    if (iter && typeof iter.return === "function") {
      try {
        await iter.return();
      } catch {
        // Iterator return rejection is fine — we're tearing down.
      }
    }
    try {
      await running;
    } catch {
      /* already handled inside loop() */
    }
  };
}

async function dispatch(s: StatusLike, cb: NATSSupervisorCallbacks): Promise<void> {
  switch (s.type) {
    case "disconnect":
      cb.log("warn", "nats disconnect", { server: stringifyData(s.data) });
      if (cb.onDisconnect) await runCallback(cb, "onDisconnect", cb.onDisconnect);
      return;
    case "reconnect":
      cb.log("warn", "nats reconnect", { server: stringifyData(s.data) });
      if (cb.onReconnect) await runCallback(cb, "onReconnect", cb.onReconnect);
      return;
    case "error":
      // Async server errors (auth violations, permission denials, etc).
      // Don't escalate — a single error doesn't mean the connection is
      // dead; `closed()` is the authority for permanent failure.
      cb.log("warn", "nats async error", { detail: stringifyData(s.data) });
      return;
    case "ldm":
      // Lame Duck Mode — the server is asking us to migrate to another
      // member of the cluster. Operators want to see it; the client
      // handles the actual migration.
      cb.log("warn", "nats lame-duck-mode signal", { server: stringifyData(s.data) });
      return;
    case "reconnecting":
    case "staleConnection":
    case "pingTimer":
    case "client initiated reconnect":
    case "update":
      // Debug-tier events. Log at info — operators investigating a
      // downtime want them visible but they're not on the warn path.
      cb.log("info", `nats event: ${s.type}`, { data: stringifyData(s.data) });
      return;
    default:
      // Unknown event types — log so we notice if nats.js adds new
      // ones we should classify.
      cb.log("info", `nats event: ${s.type}`, { data: stringifyData(s.data) });
      return;
  }
}

async function runCallback(
  cb: NATSSupervisorCallbacks,
  name: "onDisconnect" | "onReconnect",
  fn: () => void | Promise<void>,
): Promise<void> {
  try {
    await fn();
  } catch (err) {
    cb.log("error", `${name} callback threw`, {
      err: err instanceof Error ? err.message : String(err),
    });
  }
}

function stringifyData(data: unknown): string {
  if (data === undefined || data === null) return "";
  if (typeof data === "string") return data;
  if (typeof data === "number") return String(data);
  try {
    return JSON.stringify(data);
  } catch {
    return String(data);
  }
}

/**
 * Watch the connection's `closed()` promise and invoke `onClosed`
 * exactly once when it resolves.
 *
 * The nats.js promise resolves with either `void` (clean close) or an
 * `Error` (permanent failure). The sidecar's wiring distinguishes
 * "we initiated this close" from "the connection died on us" by
 * checking a flag in its own scope — this module just surfaces the
 * resolution.
 *
 * Returns the underlying promise so the caller can `await` it during
 * shutdown if needed.
 */
export function watchNATSClosed(
  nc: NATSConnLike,
  onClosed: (err: Error | undefined) => void,
): Promise<void> {
  return nc
    .closed()
    .then((result) => {
      const err = result instanceof Error ? result : undefined;
      try {
        onClosed(err);
      } catch {
        // Caller errors here are not recoverable — they're the very
        // shutdown path. Swallow rather than crashing on top of crash.
      }
    })
    .catch((err) => {
      // closed() should not reject, but defensively surface anything
      // weird as if it were a permanent failure.
      try {
        onClosed(err instanceof Error ? err : new Error(String(err)));
      } catch {
        /* see above */
      }
    });
}
