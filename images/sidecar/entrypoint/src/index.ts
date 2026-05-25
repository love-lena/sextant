/**
 * @sextant/sidecar — runtime entrypoint that boots inside every per-agent
 * container.
 *
 * Plan: plans/bootstrap.md#M10 (extended from M9 scaffolding).
 * Spec: specs/components/sidecar-image.md §"Sidecar entrypoint",
 *       specs/components/sextantd.md §"MCP server".
 *
 * Combined M9+M10 scope:
 *
 *   - Read the env-var contract sextantd sets at spawn time.
 *   - Connect to NATS (operator password path today; JWT lands at M11).
 *   - Publish `lifecycle.started`, heartbeat every 5s, drain cleanly on
 *     SIGTERM/SIGINT.
 *   - **M10**: connect to sextantd's MCP server over Streamable HTTP at
 *     `SEXTANT_MCP_URL`, presenting `Authorization: Bearer ${SEXTANT_JWT}`
 *     on every request. Call `tools/list` once on startup to confirm the
 *     server is reachable and log the tool catalog the agent can call.
 *
 * Out of M10 scope (lands in M11):
 *
 *   - Claude Code Agent SDK driver loop that *invokes* the MCP tools.
 *   - JWT-authenticated NATS connection.
 *
 * The entrypoint stays conservative about required env vars so the
 * smoke test (`docker run --rm sextant-sidecar:latest /bin/bash`)
 * doesn't need them at all — only the long-running mode
 * (`sextant-sidecar run`) does. The MCP connection is best-effort: a
 * missing `SEXTANT_JWT` or `SEXTANT_MCP_URL` logs a warning and skips
 * the connection rather than failing the sidecar.
 */

import { randomUUID } from "node:crypto";

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
  natsUrl: string;
  /** M11+: per-incarnation JWT issued by sextantd. M9 logs and ignores. */
  jwt: string | undefined;
  /** M9 fallback for the password path (M11 drops this). */
  operatorUser: string | undefined;
  operatorPassword: string | undefined;
  /** Optional — Claude SDK `--resume` (M11). */
  sessionId: string | undefined;
  /** Optional — MCP server URL (wired M11). */
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
    natsUrl: required("SEXTANT_NATS_URL"),
    jwt: process.env["SEXTANT_JWT"] || undefined,
    operatorUser: process.env["SEXTANT_OPERATOR_USER"] || undefined,
    operatorPassword: process.env["SEXTANT_OPERATOR_PASSWORD"] || undefined,
    sessionId: process.env["SEXTANT_SESSION_ID"] || undefined,
    mcpUrl: process.env["SEXTANT_MCP_URL"] || undefined,
  };
}

/**
 * Resolve NATS auth from env. M11 lands the JWT path; until then the
 * sidecar accepts an operator-style password pair as a stop-gap so the
 * full image+entrypoint surface is exercisable end-to-end before agent
 * identity exists.
 */
function buildConfig(env: SidecarEnv): ClientConfig {
  if (env.operatorPassword) {
    return {
      nats: { url: env.natsUrl },
      operator: {
        user: env.operatorUser ?? "operator",
        password: env.operatorPassword,
      },
      client: {
        connectTimeoutMs: 10_000,
        requestTimeoutMs: 30_000,
        logLevel: "info",
      },
    };
  }
  throw new Error(
    "sidecar: no NATS credentials provided. M9/M10 expect SEXTANT_OPERATOR_USER + SEXTANT_OPERATOR_PASSWORD; " +
      "M11 will accept SEXTANT_JWT for the NATS conn too. The MCP path uses SEXTANT_JWT directly today.",
  );
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
 * 5-second heartbeat, and tears down cleanly on SIGTERM/SIGINT.
 */
async function run(): Promise<void> {
  const env = readEnv();
  if (env.jwt && !env.operatorPassword) {
    log(
      "warn",
      "SEXTANT_JWT set but NATS JWT auth lands in M11; need SEXTANT_OPERATOR_USER + SEXTANT_OPERATOR_PASSWORD for the NATS conn",
    );
  }

  const config = buildConfig(env);
  const incarnationId = randomUUID();
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

  // M10: open the MCP client connection. Failure here logs but does
  // not abort the sidecar — heartbeats/lifecycle remain the contract.
  const mcpClient = await connectMCP(env);

  await publishLifecycle(client, env, incarnationId, "started");
  log("info", "lifecycle.started published");

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
          "  SEXTANT_AGENT_UUID, SEXTANT_AGENT_NAME, SEXTANT_HOST_ID, SEXTANT_NATS_URL",
          "  Plus SEXTANT_OPERATOR_USER + SEXTANT_OPERATOR_PASSWORD (M9; M11 swaps in SEXTANT_JWT).",
          "",
        ].join("\n"),
      );
      return;
    case "--version":
    case "version":
      process.stdout.write("sextant-sidecar 0.1.0 (M9 scaffold)\n");
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
