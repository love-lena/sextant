// @sextant/pi-bus — the pi extension that makes a pi coding-agent session a
// first-class sextant bus client (TASK-177; grows the TASK-176 spike). A pi
// session becomes an addressable, scoped bus participant: it holds its own SDK
// Client on its OWN scoped credential (never the operator's), wakes on inbound
// bus frames, exposes bus tools + a /set-goal command over the goals convention,
// bundles a sextant skill, and bridges its own turns/thinking/tool-calls onto a
// pi.activity bus topic so the dash renders a headless worker like any crew member.
//
// Drop this extension into a pi session (`pi -e .../dist/src/index.js`, env in
// config.ts). It is a single default-export factory, the standard pi extension
// shape (export default function(pi: ExtensionAPI)).
//
// The five spike-mandated adjustments, baked in:
//   1. IDEMPOTENT session_start — pi fires it twice per new_session; BusConnection
//      .open() is close-before-open + self-serialising, so no client leaks.
//   2. BACK-PRESSURE — WakeQueue is bounded + drop-oldest with a reserved DM slot
//      and burst-coalescing; the queue drains one per turn_end.
//   3. pi.activity OBSERVABILITY as a first-class lexicon (ActivityBridge).
//   4. LAYERED SECURITY — own scoped creds (BusConnection) + a headless block-by-
//      default tool gate (gate.ts) + author trust-tiering on the wake (trust.ts);
//      bus content is treated as an untrusted prompt-injection surface.
//   5. pi PINNED to 0.79.8 (package.json).

import { appendFileSync } from "node:fs";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import type { JSONValue, Message } from "@sextant/sdk";
import { resolveConfig } from "./config.js";
import { BusConnection, type Logger } from "./bus.js";
import { WakeQueue } from "./wake.js";
import { tierBanner, tierOf } from "./trust.js";
import { ActivityBridge } from "./activity.js";
import { registerTools } from "./tools.js";
import { registerGoalCommand } from "./goal_command.js";
import { registerGate } from "./gate.js";
import { Handoff, isHandoffDrain, type HandoffRecord } from "./handoff.js";

export default function sextantPiBus(pi: ExtensionAPI): void {
  const cfg = resolveConfig(process.env);

  // trace: one structured JSONL line per event. Always to stderr (RPC stdout
  // must stay clean JSONL); also to SEXTANT_PI_LOG when set (the driven harness
  // reads it for assertions). Best-effort — a trace failure never affects the
  // agent.
  const logPath = process.env["SEXTANT_PI_LOG"];
  const log: Logger = (event, fields = {}) => {
    const line = JSON.stringify({ t: Date.now(), event, ...fields });
    process.stderr.write(`[pi-bus] ${line}\n`);
    if (logPath) {
      try {
        appendFileSync(logPath, line + "\n");
      } catch {
        /* best-effort */
      }
    }
  };

  // The wake/back-pressure queue (adjustment 2). One per extension instance.
  const queue = new WakeQueue({ maxBuffered: cfg.maxBuffered, coalesceWindowMs: cfg.coalesceWindowMs });

  // The bus connection (adjustment 1 + 4a). onWake is the single entry every
  // inbound frame flows through, so the back-pressure policy sees them all.
  let ctxRef: ExtensionContext | undefined; // latest context, for idle checks in the wake path
  const bus = new BusConnection(cfg, log, (m) => onInbound(m));

  // The activity bridge (adjustment 3). Resolves the live client at publish time
  // (it reopens across transitions) and the current activity subject.
  const activity = new ActivityBridge({
    publisher: () => bus.getClient(),
    topicSubject: () => bus.activitySubject(),
    previewMax: cfg.previewMax,
    onError: (e) => log("activity_publish_error", { detail: e.message }),
  });

  // The managed close-and-resume handoff (TASK-178, AC#3). A pi.handoff drain frame
  // on the inbox routes here (NOT into the agent loop — control, not a task); the
  // wind-down finishes the current turn, announces relinquished, drains+closes the
  // bus client, and exits the process. Single-owner-at-a-time: while a handoff is
  // pending the wake path drops new frames so the worker doesn't pick up work while
  // it claims to be relinquishing. The deps resolve the LIVE ctx/bus each call,
  // because both reopen across a transition (the disposed-binding trap).
  const handoff = new Handoff({
    sessionId: () => ctxRef?.sessionManager.getSessionId() ?? "",
    isIdle: () => ctxRef?.isIdle() ?? true,
    announce: async (rec: HandoffRecord) => {
      const client = bus.getClient();
      if (!client) return; // no live client — the close/exit still releases the session
      await client.publish(bus.handoffSubject(), rec as unknown as JSONValue);
    },
    closeBus: () => bus.close("handoff"),
    // exit terminates the worker process so the session is RELEASED. ctx.shutdown()
    // is pi's graceful stop (it runs session_shutdown + flushes the JSONL), but in
    // RPC mode it does NOT actually exit while stdin is held open (the dispatcher
    // recipe holds stdin open for the worker's life so the bus, not stdin, drives
    // it). So we call ctx.shutdown() to flush, then guarantee termination with a
    // short-delayed process.exit — single-owner depends on the process truly going
    // away, not just the bus client closing. The delay lets pi flush the session
    // JSONL the resume reads; it is bounded so a wedged flush can't strand the
    // worker owning the session.
    exit: () => {
      try {
        ctxRef?.shutdown();
      } catch {
        /* shutdown is best-effort; the process.exit below is the guarantee */
      }
      setTimeout(() => process.exit(0), 500).unref?.();
    },
    log,
  });

  // onInbound applies the wake policy to one frame: idle → deliver now; busy →
  // buffer (bounded, drop-oldest, reserved DM slot, coalesced). The durable
  // record lives on the bus, so a dropped wake loses no content.
  function onInbound(m: Message): void {
    const client = bus.getClient();
    const selfId = client?.id() ?? "";
    if (m.frame.author === selfId) return; // never wake on our own echo
    const direct = m.subject === `msg.client.${selfId}`;

    // A pi.handoff drain is CONTROL, not a task: route it to the cooperative
    // wind-down and do NOT enqueue a wake, so the model never sees a drain as work.
    // Two guards, both load-bearing:
    //   - only a frame addressed to us DIRECTLY (the inbox) can drain us — a topic
    //     pi.handoff is someone else's announcement, not our order;
    //   - only a TRUSTED author (the principal or a verified peer) may drain us. A
    //     drain stops the worker and releases the session, so it is a privileged
    //     action; an unknown client's "drain" is treated as untrusted input and
    //     refused (it falls through to the normal wake path, where the model sees
    //     it tier-stamped as UNKNOWN and the gate applies — it cannot quietly stop
    //     the worker). Mirrors the headless tool gate: trust is the author, not the
    //     content.
    if (direct && isHandoffDrain(m.frame.record)) {
      const tier = tierOf(m.frame.author, bus.getTiers());
      if (tier === "unknown") {
        log("handoff_drain_refused", { from: m.frame.author, reason: "untrusted author may not drain" });
      } else {
        const reason = readableContent(m.frame.record);
        log("handoff_drain_received", { from: m.frame.author, tier });
        void handoff.onDrain(reason);
        return;
      }
    }

    // Once a handoff is winding down, take no new work — accepting a wake now would
    // leave the worker acting while it claims to have relinquished the session.
    if (handoff.isPending()) {
      log("inbound_dropped_handoff_pending", { subject: m.subject, from: m.frame.author });
      return;
    }

    log("inbound", { subject: m.subject, from: m.frame.author, seq: m.sequence, direct });

    if (ctxRef?.isIdle()) {
      deliver(m, 1);
      return;
    }
    const decision = queue.enqueue({
      direct,
      topic: m.subject,
      author: m.frame.author,
      seq: queue.nextSeq(),
      deliver: (count) => deliver(m, count),
    });
    log("buffered", {
      action: decision.action,
      bufferedTopic: decision.bufferedTopic,
      bufferedDirect: decision.bufferedDirect,
      droppedTotal: decision.droppedTotal,
    });
  }

  // deliver injects one frame into the agent loop as a custom message and asks pi
  // to run a turn (the wake primitive, AC#1). It stamps the author's TRUST TIER
  // (adjustment 4d) so the model and the gate weigh the content by its source —
  // bus content is untrusted input. coalescedCount > 1 notes a coalesced burst.
  function deliver(m: Message, coalescedCount: number): void {
    const tier = tierOf(m.frame.author, bus.getTiers());
    const banner = tierBanner(tier);
    const body = readableContent(m.frame.record);
    const burst = coalescedCount > 1 ? ` (${coalescedCount} new on this topic; this is the latest — read the topic to see the rest)` : "";
    log("wake_deliver", { from: m.frame.author, subject: m.subject, tier, coalescedCount });
    pi.sendMessage(
      {
        customType: "sextant-bus",
        content: `${banner}\nBus message on ${m.subject} from ${m.frame.author}${burst}:\n${body}`,
        display: true,
        details: { subject: m.subject, author: m.frame.author, tier, frameId: m.frame.id },
      },
      { triggerTurn: true },
    );
  }

  // --- lifecycle: idempotent open at session_start, drain+close at shutdown ---

  pi.on("session_start", async (event, ctx) => {
    ctxRef = ctx;
    // RESUME DETECTION. pi reports session_start reason "startup" even for a
    // --session-id launch that resumed a persisted session (it only reports
    // "resume" for the interactive --resume/--continue path), so the reason ALONE
    // is not reliable. Nor is "the session has any entries": a FRESH session already
    // carries setup entries (a model_change + a thinking_level_change) before any
    // turn. The reliable signal is a prior CONVERSATION — at least one message entry
    // (a user/assistant turn) at startup. We treat an explicit "resume" reason OR a
    // session that already has a message as a resume: the back half of the managed
    // handoff (the dispatcher re-spawned this worker on a persisted session).
    const priorMessages = safeMessageCount(ctx);
    const resumed = event.reason === "resume" || priorMessages > 0;
    log("session_start", { reason: event.reason, mode: ctx.mode, hasUI: ctx.hasUI, priorMessages, resumed });
    await bus.open(event.reason);
    // Announce that ownership returned, so the dash + the dispatcher see the session
    // re-acquired — the mirror of the relinquished announcement.
    if (resumed) {
      await handoff.announceAcquired(ctx.sessionManager.getSessionId(), "re-spawned to resume");
    }
  });

  pi.on("session_shutdown", async (event) => {
    log("session_shutdown", { reason: event.reason });
    // Drop anything buffered; the durable record stays on the bus.
    while (!queue.isEmpty()) queue.takeNext();
    await bus.close(event.reason);
  });

  // --- the turn loop: keep ctxRef fresh, bridge activity, drain one per turn ---

  pi.on("turn_start", async (event, ctx) => {
    ctxRef = ctx;
    activity.onTurnStart(event);
  });

  pi.on("turn_end", async (event, ctx) => {
    ctxRef = ctx;
    activity.onTurnEnd(event);
    flushOne(ctx);
  });

  pi.on("tool_execution_start", async (event) => {
    activity.onToolStart(event);
  });

  pi.on("tool_execution_end", async (event) => {
    activity.onToolEnd(event);
  });

  // flushOne delivers one buffered frame once the agent is idle again. Delivering
  // it re-triggers a turn whose own turn_end flushes the next — so the queue
  // drains in order, one wake per turn, never stacking unbounded turns.
  function flushOne(ctx: ExtensionContext): void {
    if (!ctx.isIdle()) return;
    const next = queue.takeNext();
    if (!next) return;
    log("flush_one", { bufferedTopic: queue.bufferedTopic(), bufferedDirect: queue.bufferedDirect() });
    next.p.deliver(next.coalescedCount);
  }

  // --- bus tools, the /set-goal command, the headless gate ---

  registerTools(pi, {
    getClient: () => bus.getClient(),
    onWake: (m) => onInbound(m),
    subscriptions: bus.runtimeSubscriptions(),
  });

  registerGoalCommand(pi, {
    getClient: () => bus.getClient(),
    defaultGoalId: cfg.goalId,
    selfId: () => bus.getClient()?.id() ?? "",
  });

  registerGate(pi, {
    enabled: cfg.gateDestructiveHeadless,
    onBlock: (toolName, reason) => {
      // Surface the block on the activity bridge so the dash sees a blocked
      // action rather than a silent no-op.
      activity.emitRaw({ $type: "pi.activity", kind: "tool_end", tool: toolName, isError: true, result: reason });
    },
  });
}

// safeMessageCount counts the conversation (message) entries the session already
// has at session_start — the intrinsic resume signal. A fresh session carries only
// setup entries (model_change, thinking_level_change); a resumed one additionally
// has user/assistant message entries. Best-effort: any failure reading the session
// manager yields 0 (treat as a fresh start), never throws into the lifecycle handler.
function safeMessageCount(ctx: ExtensionContext): number {
  try {
    return ctx.sessionManager.getEntries().filter((e) => e.type === "message").length;
  } catch {
    return 0;
  }
}

// readableContent extracts a human-legible string from an opaque frame record
// without baking in a lexicon: a `text` field if present (the chat.message
// convention), else the record serialised. Keeps content opaque (ADR-0005) while
// giving the model something legible to read.
function readableContent(record: JSONValue): string {
  if (record && typeof record === "object" && !Array.isArray(record)) {
    const text = (record as { text?: unknown }).text;
    if (typeof text === "string") return text;
  }
  return JSON.stringify(record);
}
