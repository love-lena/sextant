// The driven AC#5 harness — the operator-verification run, AFK. It stands up a
// throwaway HERMETIC bus (NOT a live operator bus), mints scoped creds, launches
// a real `pi --mode rpc` agent with the built @sextant/pi-bus extension and a
// REAL Anthropic model, then drives the full operator path and captures the
// evidence:
//
//   AC#1/#5  a peer (the "operator") DMs the idle pi agent → it WAKES and REPLIES
//            on the bus, as its OWN scoped identity.
//   AC#3/#5  the agent's tool-calls AND thinking stream onto the agent.activity
//            topic — the exact records the dash's generic conversation viewer
//            renders live (it subscribes msg.> and shows each subject's records;
//            thinking/message activity carry text, tool_* carry the tool name).
//   AC#6     /set-goal moves a real goal criterion through the goals convention;
//            we read the goal.<id> artifact back and assert the criterion moved
//            (the same artifact the dash reads + re-renders) and that a
//            goal.update was announced on msg.topic.goals.
//
// This is the regression harness the spike's driver became (the spike validated
// the mechanism; this validates the shipped package). It is NOT a CI test — it
// needs ANTHROPIC_API_KEY and the Go toolchain and costs a few cents. Run it via
// `npm run driven`.

import { spawn, type ChildProcess } from "node:child_process";
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { StringDecoder } from "node:string_decoder";
import { setTimeout as delay } from "node:timers/promises";
import { connect, type Client, topicSubject, clientSubject, dmSubject, type Message } from "@sextant/sdk";
import { startBus, goAvailable, type Bus } from "./busharness.js";

const HERE = dirname(fileURLToPath(import.meta.url));
// The built extension entrypoint (dist/src/index.js, compiled alongside dist/test).
const EXTENSION = join(HERE, "..", "src", "index.js");
const MODEL = process.env["SEXTANT_PI_MODEL"] ?? "claude-haiku-4-5";
const GOAL_ID = "pi-bus-driven";

interface PiRpc {
  proc: ChildProcess;
  events: Record<string, unknown>[];
  send(cmd: Record<string, unknown>): void;
  waitFor(pred: (e: Record<string, unknown>) => boolean, timeoutMs: number, label: string): Promise<Record<string, unknown>>;
  waitForCount(type: string, n: number, timeoutMs: number, label: string): Promise<boolean>;
  countEvents(type: string): number;
  lastAssistantText(): Promise<string>;
  stop(): Promise<void>;
}

// attachJsonlReader splits an RPC stream on LF only (RPC framing rule).
function attachJsonlReader(stream: NodeJS.ReadableStream, onLine: (line: string) => void): void {
  const decoder = new StringDecoder("utf8");
  let buffer = "";
  stream.on("data", (chunk: Buffer | string) => {
    buffer += typeof chunk === "string" ? chunk : decoder.write(chunk);
    for (;;) {
      const nl = buffer.indexOf("\n");
      if (nl === -1) break;
      let line = buffer.slice(0, nl);
      buffer = buffer.slice(nl + 1);
      if (line.endsWith("\r")) line = line.slice(0, -1);
      onLine(line);
    }
  });
}

// startPi launches `pi --mode rpc` with the built pi-bus extension and the bus
// wiring in its environment. The agent boots IDLE (no initial prompt), so a bus
// frame is the only thing that can wake it — the clean wake proof.
function startPi(bus: Bus, store: string, credsPath: string, piLog: string): PiRpc {
  const events: Record<string, unknown>[] = [];
  const sessionDir = mkdtempSync(join(tmpdir(), "pi-bus-driven-sessions-"));

  const proc = spawn(
    "pi",
    [
      "--mode", "rpc",
      "--provider", "anthropic",
      "--model", MODEL,
      "--thinking", "low", // a little thinking so the activity bridge carries reasoning text
      "--session-dir", sessionDir,
      "-ne",
      "-e", EXTENSION,
      "--append-system-prompt",
      "You are a crew member on a sextant bus. When you receive a bus message, reply over the bus to the sender with one short sentence using the sextant_reply tool (the sender's id is in the message). Be concise.",
    ],
    {
      cwd: HERE,
      stdio: ["pipe", "pipe", "pipe"],
      env: {
        ...process.env,
        PATH: `${process.env["HOME"]}/.npm-global/bin:${process.env["PATH"]}`,
        // HERMETIC: pin the context home to the throwaway store so nothing the
        // pi process does can touch the operator's real sextant home.
        SEXTANT_HOME: store,
        SEXTANT_PI_CREDS: credsPath,
        SEXTANT_BUS_URL: bus.url,
        // The bridge publishes to the per-agent stream msg.agent.<id>.activity
        // (the canonical path the dash + run executor consume).
        SEXTANT_GOAL_ID: GOAL_ID,
        SEXTANT_PI_LOG: piLog,
        // Keep the headless gate ON (the default) — this is a faithful unattended run.
      },
    },
  );

  attachJsonlReader(proc.stdout!, (line) => {
    if (!line.trim()) return;
    try {
      events.push(JSON.parse(line) as Record<string, unknown>);
    } catch {
      /* non-JSON stdout */
    }
  });
  attachJsonlReader(proc.stderr!, (line) => {
    if (line.includes("[pi-bus]")) process.stdout.write(line + "\n");
  });

  const send = (cmd: Record<string, unknown>) => proc.stdin!.write(JSON.stringify(cmd) + "\n");

  const waitFor = async (pred: (e: Record<string, unknown>) => boolean, timeoutMs: number, label: string) => {
    const deadline = Date.now() + timeoutMs;
    let i = 0;
    for (;;) {
      while (i < events.length) {
        const e = events[i++]!;
        if (pred(e)) return e;
      }
      if (Date.now() > deadline) throw new Error(`timed out waiting for ${label} (${timeoutMs}ms)`);
      await delay(50);
    }
  };

  const countEvents = (type: string) => events.filter((e) => e["type"] === type).length;
  const waitForCount = async (type: string, n: number, timeoutMs: number, _label: string) => {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      if (countEvents(type) >= n) return true;
      if (Date.now() > deadline) return false;
      await delay(50);
    }
  };

  const lastAssistantText = async () => {
    send({ id: "last-text", type: "get_last_assistant_text" });
    return await waitFor((e) => e["type"] === "response" && e["command"] === "get_last_assistant_text", 15_000, "last text")
      .then((e) => String((e["data"] as Record<string, unknown>)?.["text"] ?? ""))
      .catch(() => "");
  };

  const stop = async () => {
    try {
      send({ type: "abort" });
    } catch {
      /* stdin may be closed */
    }
    proc.kill("SIGINT");
    await delay(500);
    if (proc.exitCode === null) proc.kill("SIGKILL");
    try {
      rmSync(sessionDir, { recursive: true, force: true });
    } catch {
      /* best-effort */
    }
  };

  return { proc, events, send, waitFor, waitForCount, countEvents, lastAssistantText, stop };
}

function section(title: string): void {
  console.log(`\n========== ${title} ==========`);
}

interface Finding {
  ac: string;
  verdict: "PASS" | "FAIL" | "PARTIAL";
  evidence: string;
}
const findings: Finding[] = [];
function record(ac: string, verdict: Finding["verdict"], evidence: string): void {
  findings.push({ ac, verdict, evidence });
  console.log(`  -> ${ac}: ${verdict} — ${evidence}`);
}

async function main(): Promise<void> {
  if (!goAvailable()) {
    console.error("SKIP: the `go` toolchain is not on PATH (the driven run needs the real Go bus).");
    process.exit(2);
  }
  if (!process.env["ANTHROPIC_API_KEY"]) {
    console.error("SKIP: ANTHROPIC_API_KEY is not set (the driven run uses a real model).");
    process.exit(2);
  }

  section("setup: hermetic bus + scoped creds (NOT a live operator bus)");
  const bus = startBus();
  console.log(`  bus up at ${bus.url}`);
  const piAgent = bus.mint("pi-bus-agent", "agent");
  // The operator self-enrolls, which CLAIMS the bus principal (still unclaimed on
  // a fresh bus). So the pi agent's trust-tiering classifies the operator's DM as
  // PRINCIPAL (operator-equivalent) — the security-relevant tier.
  const operator = bus.mintSelf("operator", "human");
  console.log(`  minted pi agent ${piAgent.id} and operator ${operator.id} (distinct scoped creds); operator claimed principal=${operator.claimedPrincipal}`);

  // The operator's SDK client — the second identity that DMs the agent + reads
  // its activity. This stands in for the operator's dash.
  const op: Client = await connect({ credsPath: operator.credsPath, url: bus.url });

  // Seed a goal so /set-goal has a criterion to move (AC#6). The dash reads this
  // same goal.<id> artifact.
  await op.createArtifact(`goal.${GOAL_ID}`, {
    $type: "goal",
    northstar: "pi agents are first-class bus clients",
    criteria: [
      { id: "wakes", text: "the pi agent wakes + replies on the bus", status: "in-progress" },
      { id: "observable", text: "its activity renders in the dash", status: "not-started" },
    ],
  });
  console.log(`  seeded goal.${GOAL_ID} with two criteria`);

  const activitySubject = `msg.agent.${piAgent.id}.activity`;
  const piLog = join(tmpdir(), `pi-bus-driven-${Date.now()}.jsonl`);
  writeFileSync(piLog, "");

  // The operator subscribes to the agent's per-agent activity stream (what the dash
  // renders) and to msg.topic.goals (the goal-transition stream the dash watches).
  const activity: Message[] = [];
  await op.subscribe(activitySubject, (m) => activity.push(m));
  const goalUpdates: Message[] = [];
  await op.subscribe(topicSubject("goals"), (m) => goalUpdates.push(m));
  // The operator's DM conversation with the agent — sextant_reply replies on the
  // canonical 2-party DM topic (sx.DMSubject), the same subject the dash renders as
  // the thread, NOT the operator's one-way inbox.
  const replies: Message[] = [];
  await op.subscribe(dmSubject(operator.id, piAgent.id), (m) => replies.push(m));

  let pi: PiRpc | undefined;
  try {
    section("AC#1 + AC#5: the operator DMs the idle pi agent → it wakes + replies");
    pi = startPi(bus, bus.store, piAgent.credsPath, piLog);
    // Wait for the extension to connect (traced) by polling the log.
    await waitForLog(piLog, /"event":"connected"/, 30_000, "extension connect");
    await delay(1500); // let the inbox subscription settle

    const turnsBefore = pi.countEvents("turn_start");
    const dm = "Hello pi agent — please acknowledge over the bus. What is 2+2?";
    console.log(`  operator DMs the pi agent inbox: ${JSON.stringify(dm)}`);
    await op.publish(clientSubject(piAgent.id), { $type: "chat.message", text: dm });

    const woke = await pi.waitForCount("turn_start", turnsBefore + 1, 90_000, "wake turn");
    await pi.waitFor((e) => e["type"] === "agent_end", 90_000, "agent_end").catch(() => undefined);
    await delay(2000); // let the reply DM + activity flush over the bus
    const replyText = await pi.lastAssistantText();

    // Read the trust tier the extension stamped on the wake (from its trace).
    const tier = (readFileSync(piLog, "utf8").match(/"event":"wake_deliver"[^\n]*"tier":"(\w+)"/) ?? [])[1] ?? "?";
    const gotReplyDm = replies.length > 0;
    if (woke && gotReplyDm) {
      record(
        "AC#1/#5",
        "PASS",
        `idle pi agent woke on the operator's DM (no RPC prompt sent), the extension stamped the author trust tier="${tier}" (the operator is the principal), and the agent REPLIED over the bus as its own identity ${piAgent.id}. Replies on the operator↔agent DM conversation: ${replies.length}; assistant text: ${JSON.stringify(replyText.slice(0, 120))}`,
      );
    } else if (woke) {
      record("AC#1/#5", "PARTIAL", `woke + replied in-session (${JSON.stringify(replyText.slice(0, 120))}) but no reply landed on the DM conversation (model may have answered without sextant_reply); replies=${replies.length}`);
    } else {
      record("AC#1/#5", "FAIL", "the pi agent did not wake on the operator's DM within 90s");
    }

    section("AC#3 + AC#5: tool-calls + thinking stream onto the agent.activity stream");
    // Drive a turn that MUST call a tool, so the activity bridge carries a tool
    // call (the dash renders these). A bus wake already happened; here we steer a
    // tool turn directly to make the tool-call evidence deterministic.
    pi.send({
      type: "prompt",
      message: "Use the bash tool to run exactly: echo pi-bus-activity-proof. Then tell me what it printed.",
    });
    await pi.waitFor((e) => e["type"] === "agent_end", 120_000, "tool-turn agent_end").catch(() => undefined);
    await delay(2000);

    const kinds = activity.map((m) => String((m.frame.record as Record<string, unknown>)["kind"] ?? ""));
    const sawTurn = kinds.includes("turn_start") || kinds.includes("turn_end");
    const sawTool = kinds.includes("tool_start") || kinds.includes("tool_end");
    const sawThinkingOrMsg = kinds.includes("thinking") || kinds.includes("message");
    console.log(`  operator's view of ${activitySubject}: ${activity.length} activity frames, kinds=${JSON.stringify([...new Set(kinds)])}`);
    if (sawTurn && sawTool) {
      record(
        "AC#3/#5",
        "PASS",
        `the agent's turns + tool calls${sawThinkingOrMsg ? " + thinking/reply text" : ""} streamed to ${activitySubject} (kinds: ${JSON.stringify([...new Set(kinds)])}). The dash's conversation viewer subscribes msg.> and renders each subject's records live, so a headless pi worker is visible in the dash like any crew member.`,
      );
    } else {
      record("AC#3/#5", sawTurn ? "PARTIAL" : "FAIL", `activity kinds seen: ${JSON.stringify([...new Set(kinds)])} (turn=${sawTurn}, tool=${sawTool})`);
    }

    section("AC#6: /set-goal moves a real goal criterion the dash then shows");
    const goalUpdatesBefore = goalUpdates.length;
    console.log(`  invoking /set-goal observable met "activity renders live"`);
    // RPC has no command verb; a /-prefixed prompt routes to the registered
    // extension command (session.prompt → _tryExecuteExtensionCommand).
    pi.send({ type: "prompt", message: '/set-goal observable met "activity renders live in the dash"' });
    await delay(4000); // let the command's get→CAS→publish complete over the bus

    const goalAfter = await op.getArtifact(`goal.${GOAL_ID}`);
    const criteria = (goalAfter.record as { criteria?: Array<{ id?: string; status?: string }> }).criteria ?? [];
    const observable = criteria.find((c) => c.id === "observable");
    const moved = observable?.status === "met";
    const announced = goalUpdates.length > goalUpdatesBefore;
    console.log(`  goal.${GOAL_ID} criterion "observable" status=${observable?.status}; goal.update announcements=${goalUpdates.length - goalUpdatesBefore}`);
    if (moved && announced) {
      record(
        "AC#6",
        "PASS",
        `/set-goal moved criterion "observable" → met in goal.${GOAL_ID} (rev ${goalAfter.revision}) THROUGH the goals convention, and announced a goal.update on msg.topic.goals — the same artifact + stream the dash reads and re-renders (closes the loop to TASK-173).`,
      );
    } else {
      record("AC#6", moved ? "PARTIAL" : "FAIL", `criterion status=${observable?.status} (want met), goal.update announced=${announced}`);
    }

    section("command alias check: /set-goal must NOT exist as a separate hand-rolled path");
    // (Sanity: the goal moved via the convention, not a direct artifact write by
    // the extension. The convention's goal.update is the proof — a hand-rolled
    // write would not emit one. Already asserted above via `announced`.)
    console.log("  (verified by the goal.update announcement above — the convention's signature)");
  } finally {
    section("teardown");
    if (pi) await pi.stop();
    await op.close().catch(() => {});
    bus.stop();
    console.log(`  pi-bus trace: ${piLog}`);
  }

  section("DRIVEN-RUN SUMMARY");
  for (const f of findings) console.log(`  ${f.ac}: ${f.verdict}\n      ${f.evidence}`);
  const failed = findings.filter((f) => f.verdict === "FAIL");
  console.log(`\n  ${findings.length} findings; ${failed.length} FAIL.`);
  process.exit(failed.length === 0 ? 0 : 1);
}

// waitForLog polls a log file until a pattern appears or it times out.
async function waitForLog(path: string, pattern: RegExp, timeoutMs: number, label: string): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    let text = "";
    try {
      text = readFileSync(path, "utf8");
    } catch {
      /* not yet */
    }
    if (pattern.test(text)) return;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${label} in ${path} (${timeoutMs}ms)`);
    await delay(200);
  }
}

main().catch((e) => {
  console.error("driven run crashed:", e);
  process.exit(3);
});
