/**
 * Connect-or-exit helper. Wraps the initial NATS connect so a hard
 * failure (bad URL, refused, unreachable) exits the sidecar non-zero
 * rather than hanging. The daemon's supervisor then spawns a fresh
 * incarnation — silent hang is the worst failure mode (ticket
 * `slug:bug-sidecar-nats-disconnect-no-reconnect` §"Fix
 * shape" item 3).
 *
 * Extracted from `src/index.ts` so the wiring can be unit-tested
 * without spawning a Node process: the connect function and exit
 * function are both passed in as collaborators.
 */

/** Generic connect function — returns the sextant Client on success. */
export type ConnectFn = () => Promise<unknown>;

/** Log shim — matches index.ts's log() function signature. */
export type LogFn = (
  level: "info" | "warn" | "error",
  msg: string,
  extra?: Record<string, unknown>,
) => void;

/**
 * Exit function. In production this is `process.exit` (which never
 * returns); in tests it throws so the caller's await path doesn't
 * continue past the exit point.
 */
export type ExitFn = (code: number) => void;

export interface ConnectOrExitContext {
  /** Operator-visible NATS URL — surfaced in the failure log line. */
  natsUrl: string;
}

/**
 * Run the initial connect. On success, return the connected client.
 * On failure, log at `error` with the URL + cause, then call
 * `exit(1)`.
 *
 * The function does not `await` after calling `exit` — production
 * `process.exit` never returns; tests pass an `exit` that throws so
 * the same control-flow guarantee holds (no "happy path" code runs
 * past a failure).
 */
export async function connectOrExit(
  connect: ConnectFn,
  log: LogFn,
  exit: ExitFn,
  ctx: ConnectOrExitContext,
): Promise<unknown> {
  try {
    return await connect();
  } catch (err) {
    log("error", "nats connect failed; exiting so supervisor restarts us", {
      natsUrl: ctx.natsUrl,
      err: err instanceof Error ? err.message : String(err),
      stack: err instanceof Error ? err.stack : undefined,
    });
    exit(1);
    // exit() in production never returns. If we somehow get here
    // (testing exit shim, or a misbehaving caller passing a no-op
    // exit), rethrow so we don't silently continue with no client.
    throw err;
  }
}
