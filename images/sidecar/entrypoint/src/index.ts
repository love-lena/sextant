/**
 * @sextant/sidecar — runtime entrypoint that boots inside every per-agent
 * container.
 *
 * Plan: plans/bootstrap.md#M11 (extends the M9/M10 entrypoint).
 * Spec: specs/components/sidecar-image.md §"Sidecar entrypoint",
 *       specs/components/sextantd.md §"MCP server".
 *
 * M11 scope:
 *
 *   - Read the env-var contract sextantd sets at spawn time:
 *     SEXTANT_AGENT_UUID, SEXTANT_AGENT_NAME, SEXTANT_HOST_ID,
 *     SEXTANT_INCARNATION_ID, SEXTANT_NATS_URL,
 *     SEXTANT_NATS_USER + SEXTANT_NATS_PASSWORD (M11 stop-gap; see
 *     specs/components/nats.md §"Agent path"), SEXTANT_JWT (used for
 *     MCP only), SEXTANT_MCP_URL.
 *   - Connect to NATS using the operator user/password sextantd
 *     forwards (Option B per the M11 NATS-auth decision).
 *   - Publish `lifecycle.started` with incarnation_id from env so the
 *     KV record sextantd wrote and the bus envelope reference the same
 *     incarnation.
 *   - Heartbeat every 5s.
 *   - Subscribe to `agents.<uuid>.inbox` and log every prompt
 *     received. The Claude SDK driver loop that *acts* on prompts
 *     lands post-Phase-1.
 *   - Connect to the sextantd MCP server over Streamable HTTP with
 *     `Authorization: Bearer ${SEXTANT_JWT}`. Best-effort.
 *   - Drain cleanly on SIGTERM/SIGINT: stop the inbox subscription,
 *     publish `lifecycle.ended`, close NATS + MCP.
 *
 * The M9 SEXTANT_OPERATOR_USER/PASSWORD stop-gap is dropped in M11.
 * The MCP connection is best-effort: a missing `SEXTANT_JWT` or
 * `SEXTANT_MCP_URL` logs a warning and skips the connection rather
 * than failing the sidecar.
 */

import { Client as MCPClient } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";

import {
  ADDRESS_AGENT,
  connectWithConfig,
  KIND_HEARTBEAT,
  KIND_LIFECYCLE,
  newEnvelope,
  type Client,
  type ClientConfig,
} from "@sextant/client";

/**
 * Env vars consumed by the sidecar. The set sextantd promises to provide
 * at spawn time per `specs/components/sidecar-image.md` §"Env vars".
 */
interface SidecarEnv {
  agentUuid: string;
  agentName: string;
  hostId: string;
  incarnationId: string;
  natsUrl: string;
  natsUser: string;
  natsPassword: string;
  /** Per-incarnation JWT issued by sextantd. Consumed by MCP only in M11. */
  jwt: string | undefined;
  /** Optional — Claude SDK `--resume` (post-Phase-1). */
  sessionId: string | undefined;
  /** Optional — MCP server URL. */
  mcpUrl: string | undefined;
}

/** Spec: §"Env vars". `SEXTANT_*` namespace, set by sextantd at spawn. */
function readEnv(): SidecarEnv {
  const required = (name: string): string => {
    const v = process.env[name];
    if (!v) {
      throw new Error(
        `sidecar: env var ${name} is required (set by sextantd at spawn time)`,
      );
    }
    return v;
  };
  return {
    agentUuid: required("SEXTANT_AGENT_UUID"),
    agentName: required("SEXTANT_AGENT_NAME"),
    hostId: required("SEXTANT_HOST_ID"),
    incarnationId: required("SEXTANT_INCARNATION_ID"),
    natsUrl: required("SEXTANT_NATS_URL"),
    natsUser: required("SEXTANT_NATS_USER"),
    natsPassword: required("SEXTANT_NATS_PASSWORD"),
    jwt: process.env["SEXTANT_JWT"] || undefined,
    sessionId: process.env["SEXTANT_SESSION_ID"] || undefined,
    mcpUrl: process.env["SEXTANT_MCP_URL"] || undefined,
  };
}

/**
 * Build the NATS client config from env. M11 hand-off is per
 * specs/components/nats.md §"Agent path (M11 — stop-gap)": sidecars
 * use the operator user/password sextantd forwards. The JWT (env.jwt)
 * gates MCP only — NATS-side JWT auth is deferred.
 */
function buildConfig(env: SidecarEnv): ClientConfig {
  return {
    nats: { url: env.natsUrl },
    operator: {
      user: env.natsUser,
      password: env.natsPassword,
    },
    client: {
      connectTimeoutMs: 10_000,
      requestTimeoutMs: 30_000,
      logLevel: "info",
    },
  };
}

/** Log shim — single namespace prefix so journal/output is easy to grep. */
function log(level: "info" | "warn" | "error", msg: string, extra?: Record<string, unknown>): void {
  const line = {
    ts: new Date().toISOString(),
    level,
    component: "sextant-sidecar",
    msg,
    ...(extra ?? {}),
  };
  const stream = level === "error" ? process.stderr : process.stdout;
  stream.write(`${JSON.stringify(line)}\n`);
}

/**
 * Publish a lifecycle envelope. Builds a fresh envelope every call so
 * each transition has its own trace/span IDs (the M11 SDK driver will
 * make these children of the SDK session span).
 */
async function publishLifecycle(
  client: Client,
  env: SidecarEnv,
  incarnationId: string,
  transition: "started" | "ended",
  reason?: string,
): Promise<void> {
  const payload = {
    incarnation_id: incarnationId,
    agent_uuid: env.agentUuid,
    transition,
    state: transition === "started" ? "running" : "ended",
    ...(reason ? { reason } : {}),
  };
  const envelope = newEnvelope(
    KIND_LIFECYCLE,
    { kind: ADDRESS_AGENT, id: env.agentUuid, host: env.hostId },
    payload,
  );
  await client.publish(`agents.${env.agentUuid}.lifecycle`, envelope);
}

async function publishHeartbeat(
  client: Client,
  env: SidecarEnv,
  incarnationId: string,
  startedAt: number,
): Promise<void> {
  const payload = {
    agent_uuid: env.agentUuid,
    incarnation_id: incarnationId,
    host_id: env.hostId,
    uptime_seconds: Math.floor((Date.now() - startedAt) / 1000),
  };
  const envelope = newEnvelope(
    KIND_HEARTBEAT,
    { kind: ADDRESS_AGENT, id: env.agentUuid, host: env.hostId },
    payload,
  );
  await client.publish(`agents.${env.agentUuid}.heartbeat`, envelope);
}

/**
 * Connect to the sextantd MCP server over Streamable HTTP, presenting
 * the per-incarnation JWT as a Bearer token. Calls `tools/list` once on
 * connect to confirm the server is reachable and capture the catalog
 * the agent can call.
 *
 * The connection persists for the lifetime of the sidecar — the M11 SDK
 * driver loop will reuse it to invoke tools. Today the result is logged
 * and the client is held in scope; the returned MCPClient is closed on
 * shutdown.
 *
 * Failure handling: any error (no URL configured, no JWT, server
 * unreachable, auth failed) is logged and `null` is returned. The
 * sidecar keeps running — the MCP path is non-essential for the
 * heartbeat/lifecycle loop.
 */
async function connectMCP(env: SidecarEnv): Promise<MCPClient | null> {
  if (!env.mcpUrl) {
    log("info", "SEXTANT_MCP_URL unset; skipping MCP connection");
    return null;
  }
  if (!env.jwt) {
    log(
      "warn",
      "SEXTANT_MCP_URL set but SEXTANT_JWT missing; cannot authenticate to MCP",
      { mcpUrl: env.mcpUrl },
    );
    return null;
  }

  let url: URL;
  try {
    url = new URL(env.mcpUrl);
  } catch (err) {
    log("error", "SEXTANT_MCP_URL is not a valid URL", {
      mcpUrl: env.mcpUrl,
      err: err instanceof Error ? err.message : String(err),
    });
    return null;
  }

  const transport = new StreamableHTTPClientTransport(url, {
    requestInit: {
      headers: {
        Authorization: `Bearer ${env.jwt}`,
      },
    },
  });
  const client = new MCPClient(
    {
      name: "@sextant/sidecar",
      version: "0.1.0",
    },
    {
      capabilities: {},
    },
  );

  try {
    await client.connect(transport);
  } catch (err) {
    log("error", "MCP connect failed", {
      mcpUrl: env.mcpUrl,
      err: err instanceof Error ? err.message : String(err),
    });
    return null;
  }

  try {
    const { tools } = await client.listTools();
    log("info", "MCP connected", {
      mcpUrl: env.mcpUrl,
      toolCount: tools.length,
      tools: tools.map((t) => t.name),
    });
  } catch (err) {
    log("warn", "MCP listTools failed after connect", {
      err: err instanceof Error ? err.message : String(err),
    });
  }

  return client;
}

/** Heartbeat interval. Spec says "every N seconds"; pin at 5s for M9. */
const HEARTBEAT_INTERVAL_MS = 5_000;

/**
 * Subscribe to the agent's inbox and log every prompt received. Runs
 * in the background for the lifetime of the sidecar; iterator
 * termination is driven by client.close() (the async iterator finishes
 * when the underlying NATS subscription is closed).
 *
 * Failure to read off the iterator is logged but does not abort the
 * sidecar — the heartbeat path is the contract that keeps sextantd
 * happy.
 */
function startInboxLoop(client: Client, subject: string): void {
  void (async (): Promise<void> => {
    try {
      for await (const msg of client.subscribe(subject)) {
        if (msg.err) {
          log("warn", "inbox: bad envelope", {
            subject: msg.subject,
            err: msg.err.message,
          });
          await msg.ack();
          continue;
        }
        // The envelope is a sextant Envelope wrapping the prompt
        // payload. Log the payload kind + size; the full body would
        // bloat the log line.
        const env = msg.envelope;
        log("info", "inbox: prompt received", {
          subject: msg.subject,
          fromKind: env?.from.kind,
          fromId: env?.from.id,
          streamSeq: String(msg.streamSeq),
          payloadSize: env ? JSON.stringify(env.payload).length : 0,
        });
        await msg.ack();
      }
    } catch (err) {
      // ClientClosedError on shutdown — expected.
      const message = err instanceof Error ? err.message : String(err);
      if (!message.toLowerCase().includes("closed")) {
        log("error", "inbox loop failed", { err: message });
      }
    }
  })();
}

/**
 * Bound on how long shutdown will wait for an in-flight heartbeat tick
 * to settle before closing the NATS client. Heartbeat publishes go
 * through `nc.flush()` (see clients/typescript/src/publish.ts), so the
 * worst case is one round-trip; 2s leaves headroom without making
 * SIGTERM feel sluggish.
 */
const SHUTDOWN_TICK_WAIT_MS = 2_000;

/**
 * Race `promise` against a timer. If the timer wins, returns `false`
 * and the promise keeps running in the background (its rejection — if
 * any — is swallowed). If the promise wins, returns `true`. Used in
 * shutdown to bound how long we'll wait for an in-flight heartbeat to
 * settle before tearing down the NATS client.
 */
async function awaitOrTimeout(promise: Promise<unknown>, ms: number): Promise<boolean> {
  let timer: NodeJS.Timeout | undefined;
  const timeout = new Promise<false>((resolve) => {
    timer = setTimeout(() => resolve(false), ms);
  });
  // Swallow promise rejection in the background path so an unhandled
  // rejection doesn't fire after shutdown returns.
  const guarded = promise.then(
    () => true,
    () => true,
  );
  try {
    return await Promise.race([guarded, timeout]);
  } finally {
    if (timer) clearTimeout(timer);
  }
}

/**
 * Long-running mode. Connects, publishes `lifecycle.started`, loops on a
 * 5-second heartbeat, subscribes to the inbox subject for prompts, and
 * tears down cleanly on SIGTERM/SIGINT.
 */
async function run(): Promise<void> {
  const env = readEnv();

  const config = buildConfig(env);
  const incarnationId = env.incarnationId;
  const startedAt = Date.now();

  log("info", "sidecar starting", {
    agentUuid: env.agentUuid,
    agentName: env.agentName,
    hostId: env.hostId,
    incarnationId,
    natsUrl: env.natsUrl,
    mcpUrl: env.mcpUrl ?? null,
  });

  const client = await connectWithConfig(config);
  log("info", "nats connected");

  // M11: open the MCP client connection. Failure here logs but does
  // not abort the sidecar — heartbeats/lifecycle remain the contract.
  const mcpClient = await connectMCP(env);

  await publishLifecycle(client, env, incarnationId, "started");
  log("info", "lifecycle.started published");

  // Subscribe to the inbox so a prompt_agent call lands somewhere
  // observable. The Claude SDK driver loop that *acts* on prompts is
  // post-Phase-1; M11 logs them and ack's so the JetStream consumer
  // doesn't redeliver.
  const inboxSubject = `agents.${env.agentUuid}.inbox`;
  startInboxLoop(client, inboxSubject);

  // Heartbeat loop. setInterval keeps the event loop alive on its own.
  //
  // Shutdown ordering matters: clearInterval stops *future* ticks, but
  // a tick that setInterval already dispatched may be mid-`publish`
  // (awaiting flush) when SIGTERM arrives. If we closed the client
  // before that publish settled, the heartbeat would throw
  // ClientClosedError or silently drop. So we track the in-flight tick
  // promise and await it (bounded by SHUTDOWN_TICK_WAIT_MS) before
  // closing.
  let running = true;
  let currentTick: Promise<void> = Promise.resolve();
  const tick = async (): Promise<void> => {
    if (!running) return;
    try {
      await publishHeartbeat(client, env, incarnationId, startedAt);
    } catch (err) {
      log("error", "heartbeat publish failed", {
        err: err instanceof Error ? err.message : String(err),
      });
    }
  };
  const heartbeat = setInterval(() => {
    currentTick = tick();
  }, HEARTBEAT_INTERVAL_MS);

  // Shutdown sequence (order is load-bearing):
  //   1. Flip `running` so any newly-fired ticks no-op.
  //   2. clearInterval — no more ticks will be dispatched.
  //   3. await currentTick (bounded) — the last in-flight publish.
  //   4. publish lifecycle.ended.
  //   5. client.close() — only now is it safe.
  // Re-entrance guard via `running` means a second SIGTERM is a no-op.
  const shutdown = async (signal: NodeJS.Signals): Promise<void> => {
    if (!running) return;
    running = false;
    clearInterval(heartbeat);
    log("info", "shutdown received", { signal });

    const settled = await awaitOrTimeout(currentTick, SHUTDOWN_TICK_WAIT_MS);
    if (!settled) {
      log("warn", "heartbeat tick did not settle within shutdown budget", {
        budgetMs: SHUTDOWN_TICK_WAIT_MS,
      });
    }

    try {
      await publishLifecycle(client, env, incarnationId, "ended", `signal:${signal}`);
      log("info", "lifecycle.ended published");
    } catch (err) {
      log("error", "lifecycle.ended publish failed", {
        err: err instanceof Error ? err.message : String(err),
      });
    }
    if (mcpClient) {
      try {
        await mcpClient.close();
      } catch (err) {
        log("error", "mcp client close failed", {
          err: err instanceof Error ? err.message : String(err),
        });
      }
    }
    try {
      await client.close();
    } catch (err) {
      log("error", "client close failed", {
        err: err instanceof Error ? err.message : String(err),
      });
    }
    process.exit(0);
  };

  process.on("SIGTERM", (sig) => {
    void shutdown(sig);
  });
  process.on("SIGINT", (sig) => {
    void shutdown(sig);
  });
}

/**
 * CLI surface. Mostly `run` for production; `--help` and `--version`
 * for local introspection.
 */
async function main(): Promise<void> {
  const argv = process.argv.slice(2);
  const cmd = argv[0] ?? "run";
  switch (cmd) {
    case "run":
      await run();
      return;
    case "--help":
    case "-h":
    case "help":
      process.stdout.write(
        [
          "sextant-sidecar — runtime that boots inside the per-agent container.",
          "",
          "Usage: sextant-sidecar [run|help|version]",
          "",
          "Modes:",
          "  run      Connect to NATS, publish lifecycle.started, heartbeat until SIGTERM (default).",
          "  help     Print this message.",
          "  version  Print the sidecar version.",
          "",
          "Required env vars (run mode):",
          "  SEXTANT_AGENT_UUID, SEXTANT_AGENT_NAME, SEXTANT_HOST_ID,",
          "  SEXTANT_INCARNATION_ID, SEXTANT_NATS_URL,",
          "  SEXTANT_NATS_USER + SEXTANT_NATS_PASSWORD (M11 stop-gap; agents",
          "  share the operator NATS creds per specs/components/nats.md).",
          "  SEXTANT_JWT is required for the MCP path; SEXTANT_MCP_URL points",
          "  at the sextantd MCP HTTP endpoint.",
          "",
        ].join("\n"),
      );
      return;
    case "--version":
    case "version":
      process.stdout.write("sextant-sidecar 0.1.0 (M11)\n");
      return;
    default:
      process.stderr.write(`sextant-sidecar: unknown command ${JSON.stringify(cmd)}\n`);
      process.exit(2);
  }
}

main().catch((err: unknown) => {
  log("error", "sidecar fatal", {
    err: err instanceof Error ? err.message : String(err),
    stack: err instanceof Error ? err.stack : undefined,
  });
  process.exit(1);
});
