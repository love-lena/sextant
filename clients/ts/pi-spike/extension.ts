// SPIKE (TASK-176) — NOT the production pi-bus extension (TASK-177).
//
// A minimal pi extension that makes a pi session a first-class sextant bus
// client, built to *validate* the design before TASK-177 commits to it. It does
// the smallest thing that proves the four mechanism questions:
//
//   1. headless wake — an inbound bus frame wakes an idle pi agent
//      (pi.sendMessage(..., { triggerTurn: true }), the channel-equivalent the
//      shipped file-trigger example uses);
//   2. connection survival — the SDK Client opens at session_start and drains +
//      closes at session_shutdown, across reload / fork / resume;
//   3. back-pressure — a bounded inbound buffer with a documented drop policy on
//      a busy topic, so a flood never wedges the agent;
//   4. observability — pi's tool_execution_* / turn_* events bridged onto a bus
//      activity topic (and the same set is consumable via the RPC stream).
//
// The agent acts on its OWN scoped credentials (its .creds), never the
// operator's (AC#5). Content on the bus is opaque: we surface a human-readable
// `text` field if the record carries one, else the stringified record — no
// baked-in lexicon assumptions (primitives, not policy).
//
// Config is via environment variables so the spike driver can wire a fresh bus
// per run without a settings file:
//   SEXTANT_PI_CREDS       path to this agent's .creds (required)
//   SEXTANT_BUS_URL        bus NATS URL (or SEXTANT_BUS_JSON)
//   SEXTANT_BUS_JSON       bus.json discovery file (fallback)
//   SEXTANT_WATCH_TOPIC    optional topic to subscribe to (besides the inbox)
//   SEXTANT_ACTIVITY_TOPIC optional topic to publish the agent's activity to
//   SEXTANT_SPIKE_LOG      optional path for a structured JSONL trace (the
//                          driver reads this to make its assertions)

import { appendFileSync } from "node:fs";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import {
  connect,
  type Client,
  type Message,
  type JSONValue,
  topicSubject,
  clientSubject,
} from "@sextant/sdk";

// MAX_BUFFERED bounds the inbound queue. On a busy topic, when the agent is
// streaming and the queue is full, the OLDEST pending frame is dropped (the
// freshest signal wins — a wake is "come look at the bus", not a delivery
// guarantee; the durable record lives on the bus). This is the proposed policy
// the spike characterises (AC#3), tunable in TASK-177.
const MAX_BUFFERED = 16;

// trace writes one structured JSONL line for the driver's assertions and for a
// human reading the run. Best-effort: a trace failure never affects the agent.
function trace(event: string, fields: Record<string, unknown> = {}): void {
  const logPath = process.env["SEXTANT_SPIKE_LOG"];
  const line = JSON.stringify({ t: Date.now(), event, ...fields });
  // Always echo to stderr so a raw `pi --mode rpc` run shows the bridge working
  // even without a log file. stderr (not stdout) keeps the RPC JSONL clean.
  process.stderr.write(`[pi-spike] ${line}\n`);
  if (logPath) {
    try {
      appendFileSync(logPath, line + "\n");
    } catch {
      /* best-effort */
    }
  }
}

// readableContent extracts a human-readable string from an opaque frame record
// without assuming a lexicon: a `text` string field if present, else the record
// serialised. Keeps the bus content opaque (ADR-0005) while giving the LLM
// something legible.
function readableContent(record: JSONValue): string {
  if (record && typeof record === "object" && !Array.isArray(record)) {
    const text = (record as { text?: unknown }).text;
    if (typeof text === "string") return text;
  }
  return JSON.stringify(record);
}

export default function (pi: ExtensionAPI) {
  // The single live bus client for this session. Recreated on every
  // session_start (reload/fork/resume each tear the runtime down and back up),
  // so connection survival is "clean close + clean reopen", not "hold one
  // connection across the transition" — the question AC#2 answers.
  let client: Client | undefined;

  // The bounded inbound buffer (back-pressure, AC#3). Frames that arrive while
  // the agent is mid-turn queue here; when it goes idle we flush them.
  const inbound: Message[] = [];
  let dropped = 0;

  // wakeOrBuffer is the heart of the wake path. If the agent is idle, wake it
  // immediately. If it is busy, buffer (bounded) and let the turn-end flush
  // deliver — so an inbound flood never stacks unbounded turns.
  function wakeOrBuffer(ctx: ExtensionContext, m: Message): void {
    if (ctx.isIdle()) {
      deliver(m);
      return;
    }
    if (inbound.length >= MAX_BUFFERED) {
      inbound.shift(); // drop oldest — freshest signal wins
      dropped++;
      trace("backpressure_drop", { buffered: inbound.length, droppedTotal: dropped });
    }
    inbound.push(m);
    trace("buffered", { buffered: inbound.length });
  }

  // deliver injects one frame into the agent loop as a custom message and asks
  // pi to run a turn. This is the wake primitive (AC#1).
  function deliver(m: Message): void {
    const from = m.frame.author;
    const body = readableContent(m.frame.record);
    trace("wake_deliver", { from, subject: m.subject, frameId: m.frame.id });
    pi.sendMessage(
      {
        customType: "sextant-bus",
        content: `Bus message on ${m.subject} from ${from}:\n${body}`,
        display: true,
        details: { subject: m.subject, author: from, frameId: m.frame.id },
      },
      { triggerTurn: true },
    );
  }

  // flushBuffered delivers everything queued while busy, once the agent is idle
  // again. Called from turn_end. Delivering one wake re-triggers a turn that
  // (on its next turn_end) flushes the rest — draining the queue in order.
  function flushBuffered(ctx: ExtensionContext): void {
    if (inbound.length === 0 || !ctx.isIdle()) return;
    const m = inbound.shift()!;
    trace("flush_one", { remaining: inbound.length });
    deliver(m);
  }

  pi.on("session_start", async (event, ctx) => {
    trace("session_start", { reason: event.reason, mode: ctx.mode });

    const credsPath = process.env["SEXTANT_PI_CREDS"];
    if (!credsPath) {
      trace("config_error", { detail: "SEXTANT_PI_CREDS is required" });
      return;
    }

    // Idempotency guard. The spike found that pi fires session_start (reason
    // "new") TWICE for a single new_session in RPC mode (reproduced with a
    // trivial extension, no bus). A naive handler would open a second SDK client
    // and leak the first. Close any client we already hold before reopening, so
    // the handler is safe to run more than once per logical transition. This is a
    // hardening TASK-177 must carry forward.
    if (client) {
      trace("reopen_close_prior", { reason: event.reason });
      try {
        await client.close();
      } catch (e) {
        trace("close_error", { detail: (e as Error).message });
      }
      client = undefined;
      inbound.length = 0;
    }

    // Open the bus client on this agent's OWN scoped creds (AC#5). url wins over
    // the discovery file, matching the SDK's resolution order.
    try {
      client = await connect({
        credsPath,
        url: process.env["SEXTANT_BUS_URL"],
        connInfoPath: process.env["SEXTANT_BUS_JSON"],
        log: (msg) => trace("sdk_log", { msg }),
      });
    } catch (e) {
      trace("connect_error", { detail: (e as Error).message });
      return;
    }
    trace("connected", { id: client.id(), displayName: client.displayName() });

    // The inbox (msg.client.<id>) is auto-subscribed by the SDK on connect, but
    // the SDK delivers it internally; to act on DMs we subscribe to the inbox
    // subject explicitly through the user-facing subscribe(). A DM lands on the
    // pairwise dm topic AND the recipient's inbox; subscribing to the inbox
    // catches one-way mail addressed straight to us.
    const onFrame = (m: Message) => {
      trace("inbound", { subject: m.subject, from: m.frame.author, seq: m.sequence });
      wakeOrBuffer(ctx, m);
    };

    try {
      await client.subscribe(clientSubject(client.id()), onFrame);
      trace("subscribed", { subject: clientSubject(client.id()) });
    } catch (e) {
      trace("subscribe_error", { subject: "inbox", detail: (e as Error).message });
    }

    const watch = process.env["SEXTANT_WATCH_TOPIC"];
    if (watch) {
      try {
        await client.subscribe(topicSubject(watch), onFrame);
        trace("subscribed", { subject: topicSubject(watch) });
      } catch (e) {
        trace("subscribe_error", { subject: watch, detail: (e as Error).message });
      }
    }
  });

  // --- observability bridge (AC#4): pi's own event stream onto a bus topic ---

  function publishActivity(kind: string, fields: Record<string, unknown>): void {
    const topic = process.env["SEXTANT_ACTIVITY_TOPIC"];
    if (!topic || !client) return;
    const record: JSONValue = { $type: "pi.activity", kind, ...(fields as Record<string, JSONValue>) };
    void client.publish(topicSubject(topic), record).catch((e) => {
      trace("activity_publish_error", { detail: (e as Error).message });
    });
  }

  pi.on("turn_start", async (event) => {
    trace("turn_start", { turnIndex: event.turnIndex });
    publishActivity("turn_start", { turnIndex: event.turnIndex });
  });

  pi.on("turn_end", async (event, ctx) => {
    trace("turn_end", { turnIndex: event.turnIndex });
    publishActivity("turn_end", { turnIndex: event.turnIndex });
    flushBuffered(ctx);
  });

  pi.on("tool_execution_start", async (event) => {
    trace("tool_start", { tool: event.toolName, id: event.toolCallId });
    publishActivity("tool_start", { tool: event.toolName, toolCallId: event.toolCallId });
  });

  pi.on("tool_execution_end", async (event) => {
    trace("tool_end", { tool: event.toolName, id: event.toolCallId, isError: event.isError });
    publishActivity("tool_end", {
      tool: event.toolName,
      toolCallId: event.toolCallId,
      isError: event.isError,
    });
  });

  // --- clean handoff (AC#2): drain + close so nothing fights for the session ---

  pi.on("session_shutdown", async (event) => {
    trace("session_shutdown", { reason: event.reason, buffered: inbound.length, dropped });
    inbound.length = 0;
    if (client) {
      try {
        await client.close();
        trace("closed");
      } catch (e) {
        trace("close_error", { detail: (e as Error).message });
      }
      client = undefined;
    }
  });
}
