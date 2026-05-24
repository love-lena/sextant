/**
 * Core Client class: lifecycle (connect/close), shared state, NATS +
 * JetStream handles. Subscribe / publish / RPC / KV / query are methods
 * defined in their own files and bolted on via the prototype here.
 *
 * Mirrors pkg/client/client.go.
 */

import {
  connect as natsConnect,
  type ConnectionOptions,
  type JetStreamClient,
  type JetStreamManager,
  type NatsConnection,
} from "nats";

import { loadConfig, defaultConfigPath, validateAndFill, type ClientConfig } from "./config.js";
import { ClientClosedError } from "./errors.js";
import { subscribe, subscribeFromSeq, type SubscribeOptions, type Message } from "./subscribe.js";
import { publish } from "./publish.js";
import { rpc, type RPCOptions } from "./rpc.js";
import { query, type QueryFilter } from "./query.js";
import { getKV, putKV, watchKV, type KVUpdate } from "./kv.js";
import type { Envelope } from "./types.generated.js";

/** Options on the `connect` / `connectWithConfig` entry points. */
export interface ConnectOptions {
  /** Override the default config path (`~/.config/sextant/client.toml`). */
  configPath?: string;
  /** Caller-supplied NATS option overrides, primarily for tests. */
  natsOptions?: Partial<ConnectionOptions>;
}

/**
 * Internal handle used by every subscribe / watch worker so `close()`
 * can tear them down even if the caller never canceled their own
 * AbortController. Mirrors stopRegistration in pkg/client/client.go.
 */
export interface StopRegistration {
  /** Called once by Client.close(). Idempotent on the worker side. */
  stop: () => void;
}

/**
 * Sextant bus client. Connect via `connect()` / `connectWithConfig()`;
 * close via `close()`. Safe for concurrent use — the underlying NATS
 * connection handles internal multiplexing.
 */
export class Client {
  /** Normalized config the Client was built with. */
  readonly config: ClientConfig;

  /** @internal — NATS connection. Test-only access. */
  readonly nc: NatsConnection;
  /** @internal — JetStream client. Test-only access. */
  readonly js: JetStreamClient;
  /** @internal — JetStream manager (for stream + KV admin). Test-only. */
  readonly jsm: JetStreamManager;

  /** @internal */
  private _closed = false;
  /** @internal */
  private readonly stoppers = new Set<StopRegistration>();

  private constructor(
    config: ClientConfig,
    nc: NatsConnection,
    js: JetStreamClient,
    jsm: JetStreamManager,
  ) {
    this.config = config;
    this.nc = nc;
    this.js = js;
    this.jsm = jsm;
  }

  /** True after `close()` has been invoked. */
  get closed(): boolean {
    return this._closed;
  }

  /**
   * Throw ClientClosedError if `close()` has been called. Called by
   * every method that touches NATS.
   */
  ensureOpen(): void {
    if (this._closed) throw new ClientClosedError();
  }

  /** @internal Register a stopper. Returns a handle the worker passes back to `deregister`. */
  register(stop: () => void): StopRegistration {
    const reg: StopRegistration = { stop };
    this.stoppers.add(reg);
    return reg;
  }

  /** @internal Drop reg from tracking. Safe to call multiple times. */
  deregister(reg: StopRegistration): void {
    this.stoppers.delete(reg);
  }

  /**
   * Build the client + open NATS. Internal entry point — public API
   * is `connect()` and `connectWithConfig()`.
   */
  static async _dial(config: ClientConfig, opts?: ConnectOptions): Promise<Client> {
    const baseOpts: ConnectionOptions = {
      servers: config.nats.url,
      name: "sextant-client-ts",
      timeout: config.client.connectTimeoutMs,
      // Reconnect knobs pinned by specs/components/client-libraries.md
      // §"Shared concerns" — same as pkg/client/client.go.
      maxReconnectAttempts: -1,
      reconnectTimeWait: 500,
      reconnectJitter: 400,
    };
    if (config.operator.password) {
      baseOpts.user = config.operator.user;
      baseOpts.pass = config.operator.password;
    } else if (config.operator.credsPath) {
      // The official nats.js auth path for creds files is the
      // credsAuthenticator — defer that wiring until creds-mode is
      // exercised (M5+ writes operator.creds; M4 path is password).
      throw new Error(
        "client: operator.credsPath is not yet supported by the TS client; use operator.password (parity with pkg/client M4)",
      );
    }
    const merged: ConnectionOptions = { ...baseOpts, ...(opts?.natsOptions ?? {}) };
    const nc = await natsConnect(merged);
    const js = nc.jetstream();
    const jsm = await nc.jetstreamManager();
    return new Client(config, nc, js, jsm);
  }

  // ---- Method shims wired to per-file implementations ----

  subscribe(subject: string, opts: SubscribeOptions = {}): AsyncIterable<Message> {
    return subscribe(this, subject, opts);
  }

  subscribeFromSeq(subject: string, fromSeq: bigint): AsyncIterable<Message> {
    return subscribeFromSeq(this, subject, fromSeq);
  }

  async publish(subject: string, env: Envelope): Promise<void> {
    return publish(this, subject, env);
  }

  async rpc<Req = unknown, Resp = unknown>(
    verb: string,
    req: Req,
    opts: RPCOptions = {},
  ): Promise<Resp> {
    return rpc<Req, Resp>(this, verb, req, opts);
  }

  async query(filter: QueryFilter): Promise<Envelope[]> {
    return query(this, filter);
  }

  watchKV(bucket: string, key: string): AsyncIterable<KVUpdate> {
    return watchKV(this, bucket, key);
  }

  async getKV(bucket: string, key: string): Promise<Uint8Array> {
    return getKV(this, bucket, key);
  }

  async putKV(bucket: string, key: string, value: Uint8Array): Promise<void> {
    return putKV(this, bucket, key, value);
  }

  /**
   * Tear down the NATS connection and stop every active subscriber /
   * watcher. Idempotent: a second call is a no-op.
   *
   * After close() returns, every previously-returned AsyncIterable is
   * also closed.
   */
  async close(): Promise<void> {
    if (this._closed) return;
    this._closed = true;
    // Snapshot then clear so the stop() callbacks (which may call
    // deregister synchronously) don't mutate-during-iteration.
    const snapshot = [...this.stoppers];
    this.stoppers.clear();
    for (const s of snapshot) {
      try {
        s.stop();
      } catch {
        // Stop callbacks must not throw; if one does, ignore — we are
        // tearing down anyway.
      }
    }
    try {
      await this.nc.drain();
    } catch {
      // Drain failure on a dead connection is fine; close() below
      // covers it.
    }
    try {
      await this.nc.close();
    } catch {
      // Already closed — ignore.
    }
  }
}

/**
 * Load `~/.config/sextant/client.toml` (or `opts.configPath`) and dial
 * NATS. Mirrors pkg/client.Connect.
 */
export async function connect(opts: ConnectOptions = {}): Promise<Client> {
  const path = opts.configPath ?? defaultConfigPath();
  const cfg = await loadConfig(path);
  return connectWithConfig(cfg, opts);
}

/**
 * Dial NATS with an already-parsed config. Mirrors
 * pkg/client.ConnectWithConfig.
 */
export async function connectWithConfig(
  cfg: ClientConfig | Parameters<typeof validateAndFill>[0],
  opts: ConnectOptions = {},
): Promise<Client> {
  const normalized = isFullClientConfig(cfg) ? cfg : validateAndFill(cfg);
  return Client._dial(normalized, opts);
}

function isFullClientConfig(c: unknown): c is ClientConfig {
  if (!c || typeof c !== "object") return false;
  const obj = c as { client?: { logLevel?: string; connectTimeoutMs?: number } };
  return !!obj.client && typeof obj.client.logLevel === "string";
}
