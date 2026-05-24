/**
 * Error types for @sextant/client.
 *
 * Mirrors pkg/client/errors.go and pkg/client/rpc.go's RPCError so Go
 * and TS callers see equivalent failure surfaces.
 */

/**
 * Thrown by methods called after `Client.close()` has resolved.
 *
 * Matches pkg/client.ErrClosed.
 */
export class ClientClosedError extends Error {
  override readonly name = "ClientClosedError";

  constructor() {
    super("client: closed");
  }
}

/**
 * Thrown by `Client.getKV` when the key does not exist in the bucket.
 *
 * Matches pkg/client.ErrKVKeyNotFound. Callers check via `instanceof`.
 */
export class KVKeyNotFoundError extends Error {
  override readonly name = "KVKeyNotFoundError";

  constructor(
    public readonly bucket: string,
    public readonly key: string,
  ) {
    super(`client: kv key not found: ${bucket}/${key}`);
  }
}

/**
 * Thrown by `Client.rpc` when the per-call timeout elapses without a
 * terminal reply. Distinct from a server-side `RPCError { code: "timeout" }`
 * — that surfaces as a structured `RPCError` with code `"timeout"`.
 *
 * Matches pkg/client.ErrRPCTimeout.
 */
export class RPCTimeoutError extends Error {
  override readonly name = "RPCTimeoutError";

  constructor(public readonly verb: string, public readonly timeoutMs: number) {
    super(`client: rpc timeout: verb=${verb} after ${timeoutMs}ms`);
  }
}

/**
 * Structured error returned by the server in an `rpc_response`
 * envelope. Mirrors pkg/client.RPCError.
 *
 * Inspect via `instanceof` plus the `code` field:
 *
 * ```ts
 * try {
 *   await client.rpc("get_agent_status", { agent_id });
 * } catch (e) {
 *   if (e instanceof RPCError && e.code === "agent_not_found") { ... }
 * }
 * ```
 */
export class RPCError extends Error {
  override readonly name = "RPCError";

  constructor(
    public readonly code: string,
    message: string,
    public readonly details?: Record<string, unknown>,
  ) {
    super(`rpc ${code}: ${message}`);
  }
}
