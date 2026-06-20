// The TASK-184 capstone live demo — the single, operator-invokable, self-validating
// run that proves the whole co-equal-clients refactor's payoff works end to end on
// the operator's machine: a co-equal TypeScript pi client (own scoped identity)
// wakes on a DM, replies over the bus, moves a goal that renders, and streams its
// thinking + tool-calls to a bus activity topic the dash renders live — a headless
// pi worker the operator watches and DMs like any crew member.
//
// It REUSES the landed harnesses rather than reinventing:
//   - test/busharness.ts  the hermetic real-Go-bus bootstrap + scoped-creds mint
//                          (here with { wsListen: true } so the dash can connect).
//   - test/driven.ts       the verified pi-agent wake → reply → set-goal → activity
//                          drive pattern (the same assertions, recomposed here with
//                          the dash layer added).
//   - docs/demos/dash-direct-ws-demo.sh  the ws-listener + `dash --serve` recipe.
//
// TWO LAYERS, in one run:
//   1. SELF-VALIDATION (AC#4): a second SDK client stands in for the operator, DMs
//      the idle pi agent, and we PROGRAMMATICALLY assert every bus-side step —
//      distinct pi identity; the reply DM on the operator inbox; the goal.update +
//      the moved criterion; ≥1 pi.activity frame of each kind. Each step prints
//      PASS/FAIL; the run prints a final N/N summary and exits non-zero on any FAIL.
//   2. OPERATOR-WATCHABLE (AC#2/#3): once green, it keeps the dash + the pi agent
//      ALIVE and prints the dash URL, so the operator opens it and DMs the pi worker
//      themselves — watching it wake, reply, and stream its activity + goal live.
//
// HERMETIC: SEXTANT_HOME is pinned to a throwaway store on EVERY CLI/agent/pi env
// (busharness does this), so `register --self` never flips the operator's real
// active context. Loopback-only; torn down at the end. REAL MODEL: the pi agent
// runs a real Anthropic model (needs ANTHROPIC_API_KEY; a few cents).
//
// Run it via docs/demos/pi-live-demo.sh (which builds everything from clean), or
// `npm run live-demo` inside clients/ts/pi after an install+build.

import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import { mkdtempSync, writeFileSync, readFileSync, rmSync, existsSync, openSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { StringDecoder } from "node:string_decoder";
import { setTimeout as delay } from "node:timers/promises";
import { connect, type Client, topicSubject, clientSubject, type Message } from "@sextant/sdk";
import { startBus, goAvailable, repoRoot, type Bus } from "./busharness.js";

const HERE = dirname(fileURLToPath(import.meta.url));
// The built extension entrypoint (dist/src/index.js, compiled alongside dist/test).
const EXTENSION = join(HERE, "..", "src", "index.js");
const MODEL = process.env["SEXTANT_PI_MODEL"] ?? "claude-haiku-4-5";
const GOAL_ID = "pi-live-demo";
// How long to keep the dash + pi agent alive for the operator to watch + DM after
// the self-validation passes. Default 10 min; 0 = don't wait (pure AFK self-test).
const WATCH_MS = Number.parseInt(process.env["SEXTANT_DEMO_WATCH_MS"] ?? "600000", 10);

const C = {
  cyan: (s: string) => `\x1b[1;36m${s}\x1b[0m`,
  green: (s: string) => `\x1b[1;32m${s}\x1b[0m`,
  red: (s: string) => `\x1b[1;31m${s}\x1b[0m`,
  dim: (s: string) => `\x1b[2m${s}\x1b[0m`,
  bold: (s: string) => `\x1b[1m${s}\x1b[0m`,
};
function say(msg: string): void {
  console.log(`${C.cyan("[demo]")} ${msg}`);
}
function section(title: string): void {
  console.log(`\n${C.bold(`========== ${title} ==========`)}`);
}

interface Step {
  step: string;
  verdict: "PASS" | "FAIL";
  evidence: string;
}
const steps: Step[] = [];
function record(step: string, ok: boolean, evidence: string): void {
  const verdict = ok ? "PASS" : "FAIL";
  steps.push({ step, verdict, evidence });
  const tag = ok ? C.green(`[PASS]`) : C.red(`[FAIL]`);
  console.log(`  ${tag} ${step} — ${evidence}`);
}

interface PiRpc {
  proc: ChildProcess;
  events: Record<string, unknown>[];
  send(cmd: Record<string, unknown>): void;
  waitFor(pred: (e: Record<string, unknown>) => boolean, timeoutMs: number, label: string): Promise<Record<string, unknown>>;
  waitForCount(type: string, n: number, timeoutMs: number): Promise<boolean>;
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

// startPi launches `pi --mode rpc` with the built pi-bus extension, on the CHILD's
// OWN scoped creds (SEXTANT_PI_CREDS), against the throwaway bus — the exact shape
// the dispatcher's recipes/pi.sh launches. It boots IDLE (no initial prompt), so a
// bus frame is the only thing that can wake it: the clean headless-wake proof.
function startPi(bus: Bus, store: string, credsPath: string, piLog: string, activityTopic: string): PiRpc {
  const events: Record<string, unknown>[] = [];
  const sessionDir = mkdtempSync(join(tmpdir(), "pi-live-demo-sessions-"));

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
      "You are a headless crew member on a sextant collaboration bus with your OWN bus identity (never the operator's). When a bus message reaches you, reply over the bus to the sender with one short sentence using the sextant_reply tool (the sender's id is in the message). Be concise.",
    ],
    {
      cwd: HERE,
      stdio: ["pipe", "pipe", "pipe"],
      env: {
        ...process.env,
        PATH: `${process.env["HOME"]}/.npm-global/bin:${process.env["PATH"]}`,
        // HERMETIC: pin the context home to the throwaway store so nothing the pi
        // process does can touch the operator's real sextant home.
        SEXTANT_HOME: store,
        SEXTANT_PI_CREDS: credsPath,
        SEXTANT_BUS_URL: bus.url,
        SEXTANT_ACTIVITY_TOPIC: activityTopic,
        SEXTANT_GOAL_ID: GOAL_ID,
        SEXTANT_PI_LOG: piLog,
        // Keep the headless gate ON (the default) — a faithful unattended run.
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
    if (line.includes("[pi-bus]")) process.stdout.write(C.dim(line) + "\n");
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
  const waitForCount = async (type: string, n: number, timeoutMs: number) => {
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

// startDash launches `sextant dash --serve` against the throwaway store (which now
// carries the wsURL the browser dials), exactly like docs/demos/dash-direct-ws-demo.sh.
// It returns the dash URL read from the state file (fail-loud on no start).
function startDash(bus: Bus, store: string, stateFile: string, dashLog: string): { proc: ChildProcess; url: string } {
  const out = createWritable(dashLog);
  const proc = spawn(
    bus.bin,
    ["dash", "--serve", "--store", store, "--state-file", stateFile, "--port", "0"],
    {
      cwd: repoRoot(),
      stdio: ["ignore", out, out],
      env: { ...process.env, SEXTANT_HOME: store }, // HERMETIC, same as every other spawn
    },
  );
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    if (existsSync(stateFile)) break;
    spawnSyncSleep(0.1);
  }
  if (!existsSync(stateFile)) {
    proc.kill("SIGKILL");
    throw new Error(`dash --serve did not write ${stateFile} within 30s:\n${safeRead(dashLog)}`);
  }
  const url = (JSON.parse(readFileSync(stateFile, "utf8")) as { url: string }).url;
  return { proc, url };
}

// createWritable opens a log file as an fd usable as a spawn stdio target.
function createWritable(path: string): number {
  writeFileSync(path, "");
  return openSync(path, "a");
}
function safeRead(path: string): string {
  try {
    return readFileSync(path, "utf8");
  } catch {
    return "(no log)";
  }
}
function spawnSyncSleep(seconds: number): void {
  spawnSync("sleep", [String(seconds)]);
}

async function main(): Promise<void> {
  section("preflight");
  if (!goAvailable()) {
    console.error(C.red("SKIP: the `go` toolchain is not on PATH (the demo builds + runs the real Go bus)."));
    process.exit(2);
  }
  if (!process.env["ANTHROPIC_API_KEY"]) {
    console.error(C.red("SKIP: ANTHROPIC_API_KEY is not set (the pi agent runs a real model)."));
    process.exit(2);
  }
  say("go toolchain present; ANTHROPIC_API_KEY present (the pi agent runs a real model — a few cents)");

  section("AC#1: boot a throwaway HERMETIC bus (ws listener on) + mint a DISTINCT pi identity");
  const bus = startBus({ wsListen: true });
  say(`hermetic bus up at ${bus.url} (ws for the dash at ${bus.wsURL}); store ${C.dim(bus.store)}`);

  const piAgent = bus.mint("pi-live-demo-agent", "agent");
  // The operator self-enrolls, which CLAIMS the bus principal (unclaimed on a fresh
  // bus, ADR-0031). So the pi agent's trust-tiering classifies the operator's DM as
  // PRINCIPAL (operator-equivalent) — the security-relevant tier.
  const operator = bus.mintSelf("operator", "human");
  const distinct = piAgent.id !== operator.id && piAgent.credsPath !== operator.credsPath;
  record(
    "AC#1 distinct pi identity",
    distinct,
    `pi agent ${piAgent.id} (own scoped creds) is a DISTINCT bus identity from the operator ${operator.id}; operator claimed principal=${operator.claimedPrincipal}`,
  );

  // The operator's SDK client — the second identity that DMs the agent + reads its
  // activity. This stands in for the operator's dash for the self-validation.
  const op: Client = await connect({ credsPath: operator.credsPath, url: bus.url });

  // Seed a goal so /set-goal has a criterion to move (AC#2). The dash reads this
  // same goal.<id> artifact + re-renders it.
  await op.createArtifact(`goal.${GOAL_ID}`, {
    $type: "goal",
    northstar: "a pi agent is a first-class crew member on the operator's bus",
    criteria: [
      { id: "wakes", text: "the pi agent wakes + replies on the bus", status: "in-progress" },
      { id: "observable", text: "its activity renders live in the dash", status: "not-started" },
    ],
  });
  say(`seeded goal.${GOAL_ID} with two criteria (the dash Goals view reads this artifact)`);

  const activityTopic = `pi.activity.${piAgent.id}`;
  const piLog = join(tmpdir(), `pi-live-demo-${Date.now()}.jsonl`);
  writeFileSync(piLog, "");

  // The operator subscribes to the activity topic (what the dash renders), to
  // msg.topic.goals (the goal-transition stream the dash watches), and to its own
  // inbox (to catch the agent's reply DM via sextant_reply).
  const activity: Message[] = [];
  await op.subscribe(topicSubject(activityTopic), (m) => activity.push(m));
  const goalUpdates: Message[] = [];
  await op.subscribe(topicSubject("goals"), (m) => goalUpdates.push(m));
  const replies: Message[] = [];
  await op.subscribe(clientSubject(operator.id), (m) => replies.push(m));

  let pi: PiRpc | undefined;
  let dash: { proc: ChildProcess; url: string } | undefined;
  const stateFile = join(bus.store, "dash.json");
  const dashLog = join(tmpdir(), `pi-live-demo-dash-${Date.now()}.log`);

  try {
    section("AC#2: the operator DMs the idle pi agent → it WAKES + REPLIES over the bus");
    pi = startPi(bus, bus.store, piAgent.credsPath, piLog, activityTopic);
    await waitForLog(piLog, /"event":"connected"/, 30_000, "pi-bus extension connect");
    await delay(1500); // let the inbox subscription settle

    const turnsBefore = pi.countEvents("turn_start");
    const dm = "Hello pi agent — please acknowledge over the bus. What is 2+2?";
    say(`operator DMs the pi agent inbox: ${JSON.stringify(dm)}`);
    await op.publish(clientSubject(piAgent.id), { $type: "chat.message", text: dm });

    const woke = await pi.waitForCount("turn_start", turnsBefore + 1, 90_000);
    await pi.waitFor((e) => e["type"] === "agent_end", 90_000, "agent_end").catch(() => undefined);
    await delay(2000); // let the reply DM + activity flush over the bus
    const replyText = await pi.lastAssistantText();
    const tier = (readFileSync(piLog, "utf8").match(/"event":"wake_deliver"[^\n]*"tier":"(\w+)"/) ?? [])[1] ?? "?";
    record(
      "AC#2 wake + reply",
      woke && replies.length > 0,
      woke && replies.length > 0
        ? `the idle pi agent WOKE on the operator's DM (no RPC prompt sent), the extension stamped author tier="${tier}", and it REPLIED over the bus as its own id ${piAgent.id}: ${JSON.stringify(replyText.slice(0, 100))}`
        : `woke=${woke}, reply DMs on operator inbox=${replies.length} (the model may have answered without sextant_reply)`,
    );

    section("AC#2: /set-goal moves a real goal criterion the dash then renders");
    const goalUpdatesBefore = goalUpdates.length;
    say(`invoking /set-goal to move criterion "observable" → met`);
    pi.send({ type: "prompt", message: '/set-goal observable met "activity renders live in the dash"' });
    await delay(4000); // let the command's get→CAS→publish complete over the bus
    const goalAfter = await op.getArtifact(`goal.${GOAL_ID}`);
    const criteria = (goalAfter.record as { criteria?: Array<{ id?: string; status?: string }> }).criteria ?? [];
    const observable = criteria.find((c) => c.id === "observable");
    const moved = observable?.status === "met";
    const announced = goalUpdates.length > goalUpdatesBefore;
    record(
      "AC#2 goal renders",
      moved && announced,
      moved && announced
        ? `/set-goal moved criterion "observable" → met in goal.${GOAL_ID} (rev ${goalAfter.revision}) THROUGH the goals convention and announced a goal.update on msg.topic.goals — the same artifact + stream the dash Goals view reads + re-renders`
        : `criterion status=${observable?.status ?? "?"} (want met), goal.update announced=${announced}`,
    );

    section("AC#3: the agent's tool-calls + thinking stream to the pi.activity topic (dash renders)");
    say(`steering a tool turn so the activity bridge carries a deterministic tool-call`);
    pi.send({
      type: "prompt",
      message: "Use the bash tool to run exactly: echo pi-live-demo-activity-proof. Then tell me what it printed.",
    });
    await pi.waitFor((e) => e["type"] === "agent_end", 120_000, "tool-turn agent_end").catch(() => undefined);
    await delay(2000);
    const kinds = activity.map((m) => String((m.frame.record as Record<string, unknown>)["kind"] ?? ""));
    const uniq = [...new Set(kinds)];
    const sawTurn = kinds.includes("turn_start") || kinds.includes("turn_end");
    const sawTool = kinds.includes("tool_start") || kinds.includes("tool_end");
    const sawThinkingOrMsg = kinds.includes("thinking") || kinds.includes("message");
    record(
      "AC#3 activity streams",
      sawTurn && sawTool,
      sawTurn && sawTool
        ? `the agent's turns + tool calls${sawThinkingOrMsg ? " + thinking/reply text" : ""} streamed to msg.topic.${activityTopic} (${activity.length} frames, kinds: ${JSON.stringify(uniq)}). The dash's conversation viewer subscribes msg.> and renders each subject's records live, so this headless pi worker is visible in the dash like any crew member.`
        : `activity kinds seen: ${JSON.stringify(uniq)} (turn=${sawTurn}, tool=${sawTool})`,
    );

    section("AC#3/#4: bring up the dash --serve so the operator can watch it live");
    dash = startDash(bus, bus.store, stateFile, dashLog);
    // Assert the dash is genuinely serving (HTTP 200 on /) — the operator-watchable layer.
    const dashUp = await httpOk(dash.url);
    record(
      "AC#4 dash serving",
      dashUp,
      dashUp
        ? `sextant dash --serve is live at ${dash.url} (a co-equal TS bus client over wss; it auto-renders the pi.activity topic + the goal). The operator opens it to watch + DM the pi worker.`
        : `dash --serve did not answer 200 at ${dash.url}; log: ${safeRead(dashLog).slice(-400)}`,
    );

    section("SELF-VALIDATION SUMMARY");
    for (const s of steps) console.log(`  ${s.verdict === "PASS" ? C.green("PASS") : C.red("FAIL")}  ${s.step}\n        ${C.dim(s.evidence)}`);
    const failed = steps.filter((s) => s.verdict === "FAIL");
    const n = steps.length;
    const passed = n - failed.length;
    console.log(`\n  ${C.bold(`${passed}/${n} PASS`)}${failed.length ? C.red(`  (${failed.length} FAIL)`) : ""}`);

    if (failed.length > 0) {
      // Fail loud: the operator-watch phase only runs on a clean green.
      throw new Error(`${failed.length} step(s) FAILED — see the summary above`);
    }

    // The operator-watchable phase: keep the dash + pi agent alive so the operator
    // opens the dash and DMs the pi worker themselves. Bounded so an unattended run
    // still terminates (and torn down on Ctrl-C).
    if (WATCH_MS > 0) {
      section("OPERATOR: watch + DM the pi worker live");
      console.log(`  ${C.green("All self-validation steps PASSED.")} The bus, the pi agent, and the dash are still live.\n`);
      console.log(`  ${C.bold("Open the dash:")} ${C.cyan(dash.url)}`);
      console.log(`    • Agents view — the pi worker "${piAgent.id}" is online (a crew member, headless).`);
      console.log(`    • Open a DM to it and send a message — watch it WAKE + REPLY on the bus.`);
      console.log(`    • Its thinking + tool-calls stream live to its activity conversation.`);
      console.log(`    • Goals view — goal.${GOAL_ID} shows criterion "observable" now met.`);
      console.log(`\n  ${C.dim(`Keeping it up for ${Math.round(WATCH_MS / 60000)} min — Ctrl-C to stop (everything is torn down).`)}`);
      await waitForInterrupt(WATCH_MS);
    }
  } finally {
    section("teardown");
    if (dash) {
      dash.proc.kill("SIGINT");
      await delay(300);
      if (dash.proc.exitCode === null) dash.proc.kill("SIGKILL");
    }
    if (pi) await pi.stop();
    await op.close().catch(() => {});
    bus.stop();
    say(`pi-bus trace: ${piLog}`);
    say(`dash log: ${dashLog}`);
  }

  process.exit(steps.some((s) => s.verdict === "FAIL") ? 1 : 0);
}

// waitForInterrupt resolves after ms or on the first SIGINT/SIGTERM (so the finally
// block tears everything down cleanly when the operator Ctrl-Cs).
async function waitForInterrupt(ms: number): Promise<void> {
  await new Promise<void>((resolve) => {
    const done = () => {
      clearTimeout(timer);
      resolve();
    };
    const timer = setTimeout(done, ms);
    process.once("SIGINT", done);
    process.once("SIGTERM", done);
  });
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

// httpOk GETs a URL and returns true on a 2xx, false otherwise (bounded).
async function httpOk(url: string): Promise<boolean> {
  // Strip a trailing ?token=... query — the SPA root accepts the bare path too, and
  // the demo only needs to confirm the server is up.
  const base = url.split("?")[0]!;
  for (let i = 0; i < 30; i++) {
    try {
      const ctrl = new AbortController();
      const t = setTimeout(() => ctrl.abort(), 2000);
      const r = await fetch(base, { signal: ctrl.signal });
      clearTimeout(t);
      if (r.ok) return true;
    } catch {
      /* not up yet */
    }
    await delay(200);
  }
  return false;
}

main().catch((e) => {
  console.error(C.red(`\nlive demo failed: ${e instanceof Error ? e.message : String(e)}`));
  process.exit(steps.some((s) => s.verdict === "FAIL") ? 1 : 3);
});
