/**
 * @sextant/sidecar — runtime entrypoint that boots inside every per-agent
 * container.
 *
 * Plan: plans/phase1-complete.md (wire-up).
 * Spec: specs/components/sidecar-image.md §"Sidecar entrypoint".
 *
 * Current scope:
 *
 *   - Read the env-var contract sextantd sets at spawn time:
 *     SEXTANT_AGENT_UUID, SEXTANT_AGENT_NAME, SEXTANT_HOST_ID,
 *     SEXTANT_INCARNATION_ID, SEXTANT_NATS_URL,
 *     SEXTANT_NATS_USER + SEXTANT_NATS_PASSWORD (M11 stop-gap),
 *     SEXTANT_JWT (used for MCP only), SEXTANT_MCP_URL,
 *     SEXTANT_MODEL, SEXTANT_SESSION_ID (optional).
 *   - Connect to NATS using the operator user/password sextantd
 *     forwards (Option B per the M11 NATS-auth decision).
 *   - Publish `lifecycle.started` with incarnation_id from env.
 *   - Heartbeat every 5s.
 *   - Connect to the sextantd MCP server over Streamable HTTP with
 *     `Authorization: Bearer ${SEXTANT_JWT}`. Best-effort.
 *   - Subscribe to `agents.<uuid>.inbox`; on each prompt, drive the
 *     Claude Agent SDK and stream events as `agent_frame` envelopes
 *     to `agents.<uuid>.frames`. Publish `lifecycle.turn_ended` when
 *     the turn completes (or fails). Persist the SDK-issued session_id
 *     to NATS KV (`agent_definitions.<uuid>`) after the first turn so
 *     subsequent spawns resume the same session.
 *   - Concurrent prompts are serialized via an in-process queue.
 *   - Drain cleanly on SIGTERM/SIGINT: stop accepting new prompts,
 *     wait briefly for the in-flight turn, publish `lifecycle.ended`,
 *     close NATS + MCP.
 *
 * Driver mode:
 *   --driver=sdk   (default) — drive the real Claude Agent SDK.
 *   --driver=mock  — emit canned events; used by tests that exercise
 *                   the bus integration without an Anthropic API call.
 */

import { query as sdkQuery, type SDKMessage } from "@anthropic-ai/claude-agent-sdk";
import { Client as MCPClient } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";

import {
  ADDRESS_AGENT,
  connectWithConfig,
  KIND_AGENT_FRAME,
  KIND_HEARTBEAT,
  KVCASConflictError,
  newEnvelope,
  type Client,
  type ClientConfig,
} from "@sextant/client";

import { classifyTool } from "./classifier.js";
import { decodeInitialPrompt } from "./env.js";
import { publishLifecycle as publishLifecycleEnvelope } from "./lifecycle.js";
import { connectOrExit } from "./sidecar-connect.js";
import {
  watchNATSClosed,
  watchNATSStatus,
  type NATSConnLike,
} from "./nats-supervision.js";

/** Bucket where AgentDefinition records live. Mirrors handlers.AgentDefinitionsBucket. */
const AGENT_DEFINITIONS_BUCKET = "agent_definitions";

/** Default model when neither env nor template provided one. Mirrors specs/architecture.md §11b. */
const DEFAULT_MODEL = "claude-opus-4-7[1m]";

type DriverMode = "sdk" | "mock";

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
  /** Per-incarnation JWT issued by sextantd. Consumed by MCP only. */
  jwt: string | undefined;
  /** Optional — Claude SDK `resume` session id. */
  sessionId: string | undefined;
  /** Optional — MCP server URL. */
  mcpUrl: string | undefined;
  /** Claude model identifier passed to the SDK. */
  model: string;
  /** SDK permissionMode derived from template permission_ceiling at spawn time. */
  permissionMode: string;
  /**
   * Template-declared `initial_prompt` (decoded from
   * `SEXTANT_INITIAL_PROMPT`, which sextantd injects base64-encoded).
   * Passed to the SDK as `systemPrompt` so the charter is included on
   * every turn — not a one-shot first user message. Undefined when the
   * template omits the field.
   */
  initialPrompt: string | undefined;
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
    model: process.env["SEXTANT_MODEL"] || DEFAULT_MODEL,
    permissionMode: resolvePermissionMode(process.env["SEXTANT_PERMISSION_MODE"]),
    initialPrompt: decodeInitialPrompt(process.env["SEXTANT_INITIAL_PROMPT"]),
  };
}

/**
 * Resolve the Claude Agent SDK permissionMode from the env var injected by
 * sextantd at spawn time. Accepts "acceptEdits" and "plan". Rejects
 * "bypassPermissions" (prohibited by [[sextant-permission-ceiling]] policy)
 * and falls back to "acceptEdits" for any unrecognised value.
 *
 * "default" is accepted as-is for operator-interactive use (e.g. local
 * dev without sextantd). sextantd never emits it for non-interactive
 * container spawns; the sidecar must handle it in case the env var is
 * manually set.
 */
function resolvePermissionMode(raw: string | undefined): string {
  switch (raw) {
    case "acceptEdits":
    case "plan":
    case "default":
      return raw;
    case "bypassPermissions":
      log("warn", "SEXTANT_PERMISSION_MODE=bypassPermissions is prohibited; falling back to acceptEdits");
      return "acceptEdits";
    default:
      // Covers undefined (env var not set) and any unknown future value.
      return "acceptEdits";
  }
}

/**
 * Build the NATS client config from env. M11 hand-off is per
 * specs/components/nats.md §"Agent path (M11 — stop-gap)".
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
 * Thin wrapper around the extracted lifecycle envelope publisher so
 * the rest of this file keeps its existing signature (passing the full
 * SidecarEnv instead of the minimal LifecycleEnv). The envelope contract
 * — notably `transition=turn_ended` published at the end of every SDK
 * turn — lives in `./lifecycle.ts` so it can be tested independently.
 */
async function publishLifecycle(
  client: Client,
  env: SidecarEnv,
  incarnationId: string,
  transition: "started" | "ended" | "turn_ended",
  reason?: string,
): Promise<void> {
  return publishLifecycleEnvelope(client, env, incarnationId, transition, reason);
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
 * Publish one `agent_frame` envelope on `agents.<uuid>.frames`. The
 * payload shape mirrors `pkg/sextantproto.AgentFramePayload`.
 */
async function publishFrame(
  client: Client,
  env: SidecarEnv,
  frameKind: "assistant_text" | "tool_call" | "tool_result" | "system_note" | "error",
  body: Record<string, unknown>,
  extras: { toolName?: string; sessionId?: string } = {},
): Promise<void> {
  const payload: Record<string, unknown> = {
    frame_kind: frameKind,
    body,
  };
  if (extras.toolName) payload["tool_name"] = extras.toolName;
  if (extras.sessionId) payload["session_id"] = extras.sessionId;
  const envelope = newEnvelope(
    KIND_AGENT_FRAME,
    { kind: ADDRESS_AGENT, id: env.agentUuid, host: env.hostId },
    payload,
  );
  await client.publish(`agents.${env.agentUuid}.frames`, envelope);
}

/**
 * Connect to the sextantd MCP server over Streamable HTTP. Returns the
 * connected client + the resolved URL, or null on any failure. The
 * sidecar continues running if MCP is unavailable.
 */
async function connectMCP(env: SidecarEnv): Promise<{ client: MCPClient; url: string } | null> {
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
    { name: "@sextant/sidecar", version: "0.1.0" },
    { capabilities: {} },
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

  return { client, url: env.mcpUrl };
}

/** Heartbeat interval. Spec says "every N seconds"; pin at 5s. */
const HEARTBEAT_INTERVAL_MS = 5_000;

/** Shutdown grace for an in-flight turn. */
const SHUTDOWN_TURN_WAIT_MS = 5_000;

/** Shutdown grace for an in-flight heartbeat tick. */
const SHUTDOWN_TICK_WAIT_MS = 2_000;

/**
 * Decoded inbox payload. The publisher (pkg/rpc/handlers/prompt.go) puts
 * `{kind: "prompt", content, from}` inside the envelope payload.
 */
interface InboxPrompt {
  content: string;
  from?: string;
}

function extractPrompt(payload: unknown): InboxPrompt | null {
  if (!payload || typeof payload !== "object") return null;
  const obj = payload as Record<string, unknown>;
  const kind = typeof obj["kind"] === "string" ? (obj["kind"] as string) : "";
  if (kind && kind !== "prompt") return null;
  const content = obj["content"];
  if (typeof content !== "string" || content === "") return null;
  const from = typeof obj["from"] === "string" ? (obj["from"] as string) : undefined;
  return { content, from };
}

/**
 * Persist the SDK-issued session_id to the agent_definitions KV entry
 * so subsequent spawns resume the session.
 *
 * Uses compare-and-set against the revision returned by getKVEntry to
 * close the read-modify-write race with restart_agent or any other
 * concurrent definition writer. On a CAS conflict (10071 / "wrong last
 * sequence") we re-read once and retry; a second conflict logs +
 * gives up — the next prompt's persist will pick up the fresh
 * revision and try again. Other failures (decode, network) are also
 * best-effort: the published session_id on every agent_frame is the
 * durable source of truth.
 */
async function persistSessionID(
  client: Client,
  env: SidecarEnv,
  sessionId: string,
): Promise<void> {
  if (!sessionId) return;
  const maxAttempts = 2;
  for (let attempt = 1; attempt <= maxAttempts; attempt++) {
    try {
      const entry = await client.getKVEntry(AGENT_DEFINITIONS_BUCKET, env.agentUuid);
      const def = JSON.parse(new TextDecoder().decode(entry.value)) as Record<string, unknown>;
      const runtime = (def["runtime"] as Record<string, unknown> | undefined) ?? {};
      const existing = typeof runtime["session_id"] === "string" ? (runtime["session_id"] as string) : "";
      if (existing === sessionId) {
        return;
      }
      runtime["session_id"] = sessionId;
      def["runtime"] = runtime;
      const currentVersion =
        typeof def["version"] === "number" ? (def["version"] as number) : 0;
      def["version"] = currentVersion + 1;
      def["updated_at"] = new Date().toISOString().replace(/Z$/, "000Z");
      const enc = new TextEncoder().encode(JSON.stringify(def));
      await client.updateKV(AGENT_DEFINITIONS_BUCKET, env.agentUuid, enc, entry.revision);
      log("info", "session_id persisted", {
        sessionId,
        revision: String(entry.revision),
        attempt,
      });
      return;
    } catch (err) {
      if (err instanceof KVCASConflictError && attempt < maxAttempts) {
        log("info", "session_id persist CAS conflict; retrying", {
          sessionId,
          expectedRevision: String(err.expectedRevision),
          attempt,
        });
        continue;
      }
      log("warn", "session_id persist failed", {
        sessionId,
        attempt,
        err: err instanceof Error ? err.message : String(err),
      });
      return;
    }
  }
}

/**
 * A driver implementation knows how to handle one prompt. Both the
 * real SDK driver and the mock driver implement this. The driver is
 * responsible for publishing every frame and the terminating
 * lifecycle.turn_ended envelope.
 */
interface PromptDriver {
  /**
   * Drive one turn. Implementations should not throw — errors should
   * be surfaced as `agent_frame` (frame_kind=error) followed by
   * `lifecycle.turn_ended` with reason="error". Returns the SDK
   * session_id (when one was issued) so the caller can persist it.
   */
  runTurn(prompt: InboxPrompt): Promise<{ sessionId?: string }>;
}

/**
 * Mock driver — emits a canned event sequence that exercises every
 * frame_kind the real SDK driver publishes, so cmd/sextantd's
 * integration test covers the full bus contract without an Anthropic
 * API call. The sequence mirrors what the SDK actually produces for a
 * one-tool-call turn:
 *
 *   1. `system_note`    — `subtype: "init"` (mirrors SDKSystemMessage init).
 *   2. `assistant_text` — body.text echoes `ack: <prompt>`.
 *   3. `tool_call`      — tool_name=`mock_echo`, body.input={prompt}.
 *   4. `tool_result`    — body.result=<echoed prompt>, is_error=false.
 *   5. `lifecycle.turn_ended` — no reason on success, reason="error"
 *      when the prompt content starts with `error:` (the error-path
 *      test's trigger).
 *
 * The error trigger is intentionally prompt-driven (not env-driven) so
 * one running sidecar can exercise both the success and error paths
 * across two prompts — the alternative (spawning two sidecars) doubles
 * test wall-clock for no signal.
 *
 * The mock honours `SEXTANT_SESSION_ID` for the first turn (so the
 * persistence test can verify the runtime.session_id round-trip) and
 * mints a deterministic session_id (`mock-session-<incarnation>`)
 * otherwise. Subsequent turns reuse the same id.
 */
function newMockDriver(client: Client, env: SidecarEnv, incarnationId: string): PromptDriver {
  let sessionId = env.sessionId ?? `mock-session-${incarnationId}`;
  return {
    async runTurn(prompt: InboxPrompt): Promise<{ sessionId?: string }> {
      const isError = prompt.content.startsWith("error:");
      try {
        await publishFrame(client, env, "system_note", { subtype: "init" }, { sessionId });
        if (isError) {
          const message = `mock_error: ${prompt.content.slice("error:".length).trim()}`;
          await publishFrame(client, env, "error", { message }, { sessionId });
          await publishLifecycle(client, env, incarnationId, "turn_ended", "error");
        } else {
          const text = `ack: ${prompt.content}`;
          await publishFrame(
            client,
            env,
            "assistant_text",
            { text },
            { sessionId },
          );
          await publishFrame(
            client,
            env,
            "tool_call",
            { input: { prompt: prompt.content }, id: "mock-tool-1" },
            { sessionId, toolName: "mock_echo" },
          );
          await publishFrame(
            client,
            env,
            "tool_result",
            {
              result: prompt.content,
              is_error: false,
              tool_use_id: "mock-tool-1",
            },
            { sessionId, toolName: "mock_echo" },
          );
          await publishLifecycle(client, env, incarnationId, "turn_ended");
        }
      } catch (err) {
        log("error", "mock driver publish failed", {
          err: err instanceof Error ? err.message : String(err),
        });
      }
      return { sessionId };
    },
  };
}

/**
 * Real SDK driver — invokes `query()` from `@anthropic-ai/claude-agent-sdk`,
 * streams its events as `agent_frame` envelopes, and publishes
 * `lifecycle.turn_ended` when the turn completes (success or error).
 *
 * Session resumption: `env.sessionId` (if non-empty) is passed as
 * `options.resume`. The SDK then loads the prior conversation history.
 * After the first turn we capture the SDK's `session_id` from any
 * message that carries it; the caller persists it back to KV.
 *
 * MCP wiring: when SEXTANT_MCP_URL + SEXTANT_JWT are both set, the
 * sextantd MCP server is advertised to the SDK as an HTTP MCP server
 * named "sextant" so the agent can call sextant tools (spawn, prompt,
 * worktree_*, etc.).
 */
function newSDKDriver(
  client: Client,
  env: SidecarEnv,
  incarnationId: string,
): PromptDriver {
  let resumeId = env.sessionId;
  return {
    async runTurn(prompt: InboxPrompt): Promise<{ sessionId?: string }> {
      let observedSessionId: string | undefined;
      const errors: string[] = [];

      const sdkOpts: Record<string, unknown> = {
        model: env.model,
        // Set permissionMode so the SDK doesn't fall back to interactive
        // "default" mode. In non-interactive containers there is no granter,
        // so leaving this unset causes every Edit/Write/Bash call to error
        // with "you haven't granted it yet". See plans/issues/bug-sidecar-
        // doesnt-set-permission-mode.md.
        permissionMode: env.permissionMode,
        // SDK + MCP defer-loading interplay: without alwaysLoad the
        // sextant tools land behind tool search, which costs a turn.
        // Always load them so simple "what tools do you have" prompts
        // see them immediately.
      };
      if (env.initialPrompt) {
        // Pass the template's `initial_prompt` as the SDK's
        // `systemPrompt`. The SDK supports `string | string[] | preset`;
        // a plain string installs it as a custom system prompt that the
        // model sees on every turn — the "persistent charter" semantic
        // the issue calls out, not a one-shot first user message. See
        // plans/issues/bug-initial-prompt-not-forwarded-to-sdk.md.
        sdkOpts["systemPrompt"] = env.initialPrompt;
      }
      if (resumeId) {
        sdkOpts["resume"] = resumeId;
      }
      if (env.mcpUrl && env.jwt) {
        sdkOpts["mcpServers"] = {
          sextant: {
            type: "http",
            url: env.mcpUrl,
            headers: { Authorization: `Bearer ${env.jwt}` },
            alwaysLoad: true,
          },
        };
      }

      // canUseTool — mirrors Claude Code CLI's `--permission-mode auto`.
      // The SDK's `acceptEdits` permissionMode already auto-allows file edits
      // but treats Bash as requiring per-call approval, which blocks agents in
      // non-interactive containers. This callback extends that to Bash with a
      // safe-command classifier: allows git/make/go/etc., denies bright-line
      // destructive patterns (rm -rf /, sudo, curl|sh, …).
      // See plans/issues/bug-sidecar-bash-still-asks-in-acceptedits.md.
      sdkOpts["canUseTool"] = (
        toolName: string,
        input: Record<string, unknown>,
      ) => {
        const decision = classifyTool(toolName, input);
        if (decision.behavior === "deny") {
          log("warn", "canUseTool: denied", {
            tool: toolName,
            reason: decision.message,
          });
        }
        return decision;
      };

      try {
        // The SDK supports `prompt` as either a string or an async
        // iterable of user messages. We pass a string — one prompt,
        // one query, one turn.
        const iterator = sdkQuery({
          prompt: prompt.content,
          options: sdkOpts as never,
        });
        for await (const msg of iterator) {
          observedSessionId = handleSDKMessage(msg, client, env, observedSessionId, errors);
        }
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        log("error", "SDK driver failed", { err: message });
        try {
          await publishFrame(
            client,
            env,
            "error",
            { message },
            { sessionId: observedSessionId },
          );
        } catch (pubErr) {
          log("error", "error-frame publish failed", {
            err: pubErr instanceof Error ? pubErr.message : String(pubErr),
          });
        }
        try {
          await publishLifecycle(client, env, incarnationId, "turn_ended", "error");
        } catch (pubErr) {
          log("error", "turn_ended publish failed", {
            err: pubErr instanceof Error ? pubErr.message : String(pubErr),
          });
        }
        return { sessionId: observedSessionId };
      }

      const turnReason = errors.length > 0 ? "error" : undefined;
      try {
        await publishLifecycle(client, env, incarnationId, "turn_ended", turnReason);
      } catch (err) {
        log("error", "turn_ended publish failed", {
          err: err instanceof Error ? err.message : String(err),
        });
      }
      // Lock in the session id for subsequent prompts in this incarnation.
      if (observedSessionId) {
        resumeId = observedSessionId;
      }
      return { sessionId: observedSessionId };
    },
  };
}

/**
 * Translate one SDK message into bus envelopes. Returns the running
 * session_id (the SDK reports it on every message-bearing event). Each
 * publish is fire-and-forget — a failure inside the loop is recorded
 * via the `errors` array but does not abort streaming.
 */
function handleSDKMessage(
  msg: SDKMessage,
  client: Client,
  env: SidecarEnv,
  currentSessionId: string | undefined,
  errors: string[],
): string | undefined {
  // Capture session id from any message that carries it.
  let sessionId = currentSessionId;
  if ("session_id" in msg && typeof msg.session_id === "string" && msg.session_id) {
    sessionId = msg.session_id;
  }

  const publish = (
    kind: "assistant_text" | "tool_call" | "tool_result" | "system_note" | "error",
    body: Record<string, unknown>,
    extras: { toolName?: string } = {},
  ): void => {
    publishFrame(client, env, kind, body, { ...extras, sessionId }).catch((err) => {
      const m = err instanceof Error ? err.message : String(err);
      errors.push(m);
      log("error", "frame publish failed", { kind, err: m });
    });
  };

  switch (msg.type) {
    case "assistant": {
      // BetaMessage.content is an array of content blocks. We project
      // text blocks → assistant_text frames and tool_use blocks →
      // tool_call frames so the bus has a normalized view.
      const content = (msg.message as { content?: unknown }).content;
      if (Array.isArray(content)) {
        for (const block of content) {
          if (!block || typeof block !== "object") continue;
          const b = block as Record<string, unknown>;
          const blockType = typeof b["type"] === "string" ? (b["type"] as string) : "";
          if (blockType === "text" && typeof b["text"] === "string") {
            publish("assistant_text", { text: b["text"] as string });
          } else if (blockType === "tool_use") {
            const toolName = typeof b["name"] === "string" ? (b["name"] as string) : "";
            publish(
              "tool_call",
              { input: b["input"] ?? {}, id: b["id"] ?? "" },
              { toolName },
            );
          }
        }
      }
      if (msg.error) {
        publish("error", { message: `assistant_error: ${msg.error}` });
        errors.push(msg.error);
      }
      return sessionId;
    }
    case "user": {
      // User messages from the SDK carry tool_result blocks (the SDK
      // synthesizes a user message wrapping the tool result before
      // feeding it back to the model). Surface those as tool_result
      // frames.
      const content = (msg.message as { content?: unknown }).content;
      if (Array.isArray(content)) {
        for (const block of content) {
          if (!block || typeof block !== "object") continue;
          const b = block as Record<string, unknown>;
          if (b["type"] === "tool_result") {
            publish(
              "tool_result",
              {
                result: b["content"] ?? null,
                is_error: Boolean(b["is_error"]),
                tool_use_id: b["tool_use_id"] ?? "",
              },
            );
          }
        }
      }
      return sessionId;
    }
    case "result": {
      // Terminal message for one turn. SDKResultError has is_error=true
      // + an errors array; SDKResultSuccess carries the final assistant
      // text in `result`. We surface errors but do NOT publish another
      // assistant_text frame here — every chunk of model text already
      // came through as an `assistant` message above.
      const r = msg as { is_error?: boolean; errors?: string[]; subtype?: string };
      if (r.is_error) {
        const detail = (r.errors ?? []).join("; ") || (r.subtype ?? "unknown");
        publish("error", { message: `sdk_result_error: ${detail}` });
        errors.push(detail);
      }
      return sessionId;
    }
    case "system": {
      // init / compact_boundary etc. Surface as a system_note so the
      // bus has the full transcript without forcing every consumer
      // to know the SDK schema.
      const subtype = (msg as { subtype?: string }).subtype ?? "system";
      publish("system_note", { subtype });
      return sessionId;
    }
    default:
      // Other event types (partial messages, hook events, etc.) are
      // not forwarded — they're either redundant with the assistant
      // event or out of scope for the initial wire-up.
      return sessionId;
  }
}

/**
 * Race `promise` against a timer. If the timer wins, returns `false`
 * and the promise keeps running in the background.
 */
async function awaitOrTimeout(promise: Promise<unknown>, ms: number): Promise<boolean> {
  let timer: NodeJS.Timeout | undefined;
  const timeout = new Promise<false>((resolve) => {
    timer = setTimeout(() => resolve(false), ms);
  });
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
 * Prompt queue. Serializes concurrent inbox prompts so the SDK sees
 * one turn at a time. Pending prompts wait their turn; the queue is
 * drained on shutdown after the current turn completes (bounded by
 * SHUTDOWN_TURN_WAIT_MS).
 */
class PromptQueue {
  private readonly pending: InboxPrompt[] = [];
  private current: Promise<void> = Promise.resolve();
  private running = true;

  constructor(
    private readonly driver: PromptDriver,
    private readonly onSessionID: (id: string) => Promise<void>,
  ) {}

  enqueue(prompt: InboxPrompt): void {
    if (!this.running) {
      log("warn", "prompt arrived after shutdown; dropping", {
        size: prompt.content.length,
      });
      return;
    }
    this.pending.push(prompt);
    this.current = this.current.then(() => this.drain());
  }

  /** Wait for the in-flight turn (if any) up to ms milliseconds. */
  async settle(ms: number): Promise<boolean> {
    this.running = false;
    return awaitOrTimeout(this.current, ms);
  }

  private async drain(): Promise<void> {
    while (this.pending.length > 0) {
      const next = this.pending.shift();
      if (!next) break;
      try {
        const { sessionId } = await this.driver.runTurn(next);
        if (sessionId) {
          await this.onSessionID(sessionId);
        }
      } catch (err) {
        // Drivers should not throw; treat as a fatal turn failure
        // but keep the queue alive for the next prompt.
        log("error", "driver runTurn threw", {
          err: err instanceof Error ? err.message : String(err),
        });
      }
    }
  }
}

/**
 * Inbox-loop controller. Exposes `restart()` so the reconnect handler
 * (see `watchNATSStatus`) can re-arm the JetStream subscription after
 * the server reappears — nats.js's ephemeral pull consumers don't
 * survive a server restart, so the previous loop's iterator will have
 * ended and a fresh subscribe is required to resume delivery.
 *
 * Regresses plans/issues/bug-sidecar-nats-disconnect-no-reconnect.md
 * §"Fix shape" item 2.
 */
interface InboxLoopController {
  /** Re-arm the loop. Idempotent: a no-op if a loop is already running. */
  restart(): void;
  /** Stop the loop; new prompts will be dropped. Used by shutdown. */
  stop(): void;
}

/**
 * Subscribe to the agent's inbox and feed every prompt into the queue.
 * Runs in the background for the lifetime of the sidecar.
 *
 * `deliverAll` semantics close a small but real race: the test (or
 * operator) sees `lifecycle.started` and immediately publishes a
 * prompt, but the JetStream consumer for the inbox is created lazily
 * the first time the iterator's `next()` is called — there's a sub-ms
 * window where the prompt could land in the stream before the consumer
 * exists, and a `deliver_policy: new` consumer would miss it. The
 * inbox stream's 24h MaxAge bounds replay; JetStream's ack semantics
 * prevent double-processing.
 *
 * After a reconnect the same `deliverAll` policy ensures any prompts
 * that landed while the sidecar was disconnected are picked up — the
 * fresh consumer replays from the start of the inbox stream, which
 * (modulo the stream's MaxAge) covers the gap.
 */
function startInboxLoop(client: Client, subject: string, queue: PromptQueue): InboxLoopController {
  let stopped = false;
  let inFlight = false;

  const runOnce = async (): Promise<void> => {
    if (stopped || inFlight) return;
    inFlight = true;
    try {
      for await (const msg of client.subscribe(subject, { deliverAll: true })) {
        if (stopped) break;
        if (msg.err) {
          log("warn", "inbox: bad envelope", {
            subject: msg.subject,
            err: msg.err.message,
          });
          await msg.ack();
          continue;
        }
        const env = msg.envelope;
        const prompt = extractPrompt(env?.payload);
        if (!prompt) {
          log("warn", "inbox: payload not a prompt", {
            subject: msg.subject,
            streamSeq: String(msg.streamSeq),
          });
          await msg.ack();
          continue;
        }
        log("info", "inbox: prompt queued", {
          subject: msg.subject,
          fromKind: env?.from.kind,
          fromId: env?.from.id,
          streamSeq: String(msg.streamSeq),
          contentLen: prompt.content.length,
        });
        queue.enqueue(prompt);
        await msg.ack();
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      if (!stopped && !message.toLowerCase().includes("closed")) {
        // The loop ending unexpectedly while we're meant to be live is
        // the canonical "JetStream consumer lost across a reconnect"
        // symptom. We log at warn (operators want it visible) and let
        // the reconnect handler re-arm us — or, if no reconnect comes,
        // the watchNATSClosed path triggers a non-zero exit.
        log("warn", "inbox loop ended; waiting for reconnect to re-arm", {
          err: message,
        });
      }
    } finally {
      inFlight = false;
    }
  };

  // Start the first iteration in the background.
  void runOnce();

  return {
    restart(): void {
      if (stopped) return;
      if (inFlight) {
        // A previous iteration is still alive — the new subscribe call
        // it's wrapped around will pick up any backlog. Avoid a second
        // concurrent loop that would double-deliver from `deliverAll`.
        return;
      }
      log("info", "inbox: re-arming subscription after reconnect", {
        subject,
      });
      void runOnce();
    },
    stop(): void {
      stopped = true;
    },
  };
}

/**
 * Long-running mode. Connects, publishes lifecycle.started, subscribes
 * to the inbox, drives the SDK on each prompt, and tears down cleanly
 * on SIGTERM/SIGINT.
 */
async function run(driverMode: DriverMode): Promise<void> {
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
    model: env.model,
    driver: driverMode,
    resumeSessionId: env.sessionId ?? null,
  });

  // Surface the resolved initial_prompt so operators can verify the
  // wiring from the template through SEXTANT_INITIAL_PROMPT into the
  // SDK's systemPrompt landed. First 80 chars + total length only —
  // charters can be long, and we don't want to dump a full charter to
  // the journal on every spawn. See
  // plans/issues/bug-initial-prompt-not-forwarded-to-sdk.md.
  if (env.initialPrompt) {
    log("info", "initial_prompt loaded", {
      length: env.initialPrompt.length,
      preview: env.initialPrompt.slice(0, 80),
    });
  } else {
    log("info", "initial_prompt not set");
  }

  // Initial connect — if this fails (bad URL, unreachable, auth
  // rejected at handshake) the sidecar must exit non-zero so the
  // daemon's supervisor spawns a fresh incarnation rather than the
  // process hanging silently. Reconnect handling (for transient
  // failures *after* a successful initial connect) lives in
  // watchNATSStatus / watchNATSClosed below. See
  // plans/issues/bug-sidecar-nats-disconnect-no-reconnect.md §"Fix
  // shape" item 3.
  const client = (await connectOrExit(
    () => connectWithConfig(config),
    log,
    (code) => process.exit(code),
    { natsUrl: env.natsUrl },
  )) as Client;
  log("info", "nats connected");

  const mcp = await connectMCP(env);

  await publishLifecycle(client, env, incarnationId, "started");
  log("info", "lifecycle.started published");

  const driver: PromptDriver =
    driverMode === "mock"
      ? newMockDriver(client, env, incarnationId)
      : newSDKDriver(client, env, incarnationId);

  const queue = new PromptQueue(driver, async (sessionId) => {
    await persistSessionID(client, env, sessionId);
  });

  const inboxSubject = `agents.${env.agentUuid}.inbox`;
  const inboxLoop = startInboxLoop(client, inboxSubject, queue);

  // Watch the underlying NATS connection's status stream so:
  //
  //   - disconnect/reconnect events land in the journal at `warn`
  //     (operators investigating a downtime need them visible — the
  //     previous behaviour was no log at all);
  //   - the inbox JetStream consumer is re-armed on reconnect (nats.js
  //     ephemeral consumers don't survive a server restart).
  //
  // Ticket: plans/issues/bug-sidecar-nats-disconnect-no-reconnect.md.
  const statusStop = watchNATSStatus(client.nc as unknown as NATSConnLike, {
    log,
    onReconnect: () => {
      inboxLoop.restart();
    },
  });

  // Watch for a permanent close. The @sextant/client base config pins
  // maxReconnectAttempts: -1, so this should only resolve when:
  //   (a) we initiate the close ourselves (graceful shutdown — the
  //       `shuttingDown` flag suppresses the exit), or
  //   (b) the NATS client gives up for an unrecoverable reason (auth
  //       rejected mid-session, manual nc.close() from elsewhere, etc).
  // Case (b) is the silent-hang failure mode the ticket calls out;
  // exiting non-zero hands control back to the daemon's supervisor.
  let shuttingDown = false;
  void watchNATSClosed(client.nc as unknown as NATSConnLike, (err) => {
    if (shuttingDown) return;
    log("error", "nats connection closed unexpectedly; exiting non-zero", {
      err: err ? err.message : "(no error reported)",
    });
    process.exit(1);
  });

  // Heartbeat loop. Same in-flight settle pattern as the M11 scaffold.
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

  const shutdown = async (signal: NodeJS.Signals): Promise<void> => {
    if (!running) return;
    running = false;
    // Flip the flag *before* anything that might close the NATS
    // connection so the `watchNATSClosed` callback knows our shutdown
    // initiated the close (and doesn't trigger a non-zero exit on top
    // of the graceful one).
    shuttingDown = true;
    clearInterval(heartbeat);
    inboxLoop.stop();
    log("info", "shutdown received", { signal });

    // Wait for the in-flight turn (best-effort).
    const drained = await queue.settle(SHUTDOWN_TURN_WAIT_MS);
    if (!drained) {
      log("warn", "turn did not settle within shutdown budget", {
        budgetMs: SHUTDOWN_TURN_WAIT_MS,
      });
    }

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
    if (mcp) {
      try {
        await mcp.client.close();
      } catch (err) {
        log("error", "mcp client close failed", {
          err: err instanceof Error ? err.message : String(err),
        });
      }
    }
    // Stop the status watcher before closing the connection so the
    // iterator's natural end (when nc.close() runs) doesn't race the
    // shutdown path.
    try {
      await statusStop();
    } catch {
      /* statusStop swallows internally; defensive guard. */
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
 * Parse the `--driver=<mode>` flag from argv. Default: `sdk`.
 */
function parseDriverMode(argv: string[]): DriverMode {
  for (const arg of argv) {
    if (arg.startsWith("--driver=")) {
      const v = arg.slice("--driver=".length);
      if (v === "sdk" || v === "mock") return v;
      throw new Error(`sextant-sidecar: --driver=${v} is invalid (sdk|mock)`);
    }
  }
  // Also honour SEXTANT_DRIVER for tests that set it via env (the
  // entrypoint.sh script doesn't forward argv beyond `run`).
  const envDriver = process.env["SEXTANT_DRIVER"];
  if (envDriver === "sdk" || envDriver === "mock") return envDriver;
  return "sdk";
}

/**
 * CLI surface.
 */
async function main(): Promise<void> {
  const argv = process.argv.slice(2);
  const cmd = argv[0] ?? "run";
  switch (cmd) {
    case "run": {
      const mode = parseDriverMode(argv.slice(1));
      await run(mode);
      return;
    }
    case "--help":
    case "-h":
    case "help":
      process.stdout.write(
        [
          "sextant-sidecar — runtime that boots inside the per-agent container.",
          "",
          "Usage: sextant-sidecar [run [--driver=sdk|mock] | help | version]",
          "",
          "Modes:",
          "  run      Connect to NATS, drive the Claude Agent SDK on each prompt.",
          "  help     Print this message.",
          "  version  Print the sidecar version.",
          "",
          "Required env vars (run mode):",
          "  SEXTANT_AGENT_UUID, SEXTANT_AGENT_NAME, SEXTANT_HOST_ID,",
          "  SEXTANT_INCARNATION_ID, SEXTANT_NATS_URL,",
          "  SEXTANT_NATS_USER + SEXTANT_NATS_PASSWORD (M11 stop-gap),",
          "  SEXTANT_MODEL (defaults to claude-opus-4-7[1m]).",
          "  SEXTANT_JWT + SEXTANT_MCP_URL light up the MCP path.",
          "  SEXTANT_SESSION_ID resumes a prior SDK session.",
          "  SEXTANT_DRIVER=mock substitutes a canned-event driver.",
          "",
        ].join("\n"),
      );
      return;
    case "--version":
    case "version":
      process.stdout.write("sextant-sidecar 0.2.0 (SDK driver wire-up)\n");
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
