// The DISPATCHER-FAITHFUL managed step-done repro. Unlike driven_stepdone (which
// spawns the pi worker by running the recipe directly), this drives the REAL
// `sextant-dispatch` binary: a coordinator-style client publishes a spawn.request
// on msg.topic.spawn; the dispatcher mints the child, launches the recipe via its
// own launchHarness, AND subscribes the child's inbox/DM for revive (manage()).
// That manage() wiring + the dispatcher lifecycle is the one thing driven_stepdone
// skipped — and the team lead's evidence says report() never fires for a
// dispatcher-spawned worker, so the dispatcher path is exactly what must be tested.
//
// It then watches the run-events subject (what coordinator.awaitStepDone awaits)
// and asserts the step-done run.event lands. If it does NOT, the live defect is
// reproduced ON THE REAL MANAGED PATH (a dispatcher-spawned worker).
//
// Needs: ANTHROPIC_API_KEY, the Go toolchain (builds sextant + sextant-dispatch),
// pi on PATH, and (for sandbox mode) the srt runtime. Run:
//   npm run build && node dist/test/driven_dispatched.js
// Env: REPRO_SANDBOX_MODE=sandbox|automode (default automode), SEXTANT_PI_MODEL.

import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { connect, type Client, type Message, topicSubject } from "@sextant/sdk";
import { startBus, goAvailable, repoRoot } from "./busharness.js";

const REPO = repoRoot();
const RECIPE = join(REPO, "clients", "sextant-cli", "internal", "components", "embed", "pi.sh");
const BUNDLE = join(REPO, "clients", "sextant-cli", "internal", "components", "embed", "pi-bus.bundle.mjs");
const MODEL = process.env["SEXTANT_PI_MODEL"] ?? "claude-haiku-4-5";

function log(...a: unknown[]) {
  console.log("[dispatched]", ...a);
}

function run(cmd: string, args: string[], opts: { cwd?: string } = {}): string {
  const r = spawnSync(cmd, args, { encoding: "utf8", cwd: opts.cwd });
  if (r.status !== 0) throw new Error(`${cmd} ${args.join(" ")} failed (${r.status}): ${r.stdout}\n${r.stderr}`);
  return r.stdout ?? "";
}

async function main() {
  if (!goAvailable()) throw new Error("Go toolchain required");
  if (!process.env["ANTHROPIC_API_KEY"]) throw new Error("ANTHROPIC_API_KEY required");

  // Build the real dispatcher binary (the bundle is materialized fresh below).
  const dispatchBin = join(REPO, "clients", "pi-bus", "dist", "test", ".sextant-dispatch");
  run("go", ["build", "-o", dispatchBin, "./clients/dispatcher"], { cwd: REPO });

  const bus = startBus();
  const cleanups: Array<() => void> = [() => bus.stop()];
  try {
    // Identities: a dispatcher (kind=dispatcher so --on-behalf mint works) and a
    // coordinator (the run owner that awaits the step-done).
    const dispatcher = bus.mint("dispatcher", "dispatcher");
    const coord = bus.mint("coordinator", "agent");
    const coordClient: Client = await connect({ credsPath: coord.credsPath, connInfoPath: join(bus.store, "bus.json") });
    cleanups.push(() => void coordClient.close());

    const runID = "01KWDISPATCH00000000000000";
    const stepID = "n1dispatchstep";
    const runEventsSubject = `msg.workflow.run.${runID}.events`;
    const spawnSubject = "msg.topic.spawn";

    const received: Message[] = [];
    await coordClient.subscribe(runEventsSubject, (m) => {
      log("EVENT on run-events:", JSON.stringify(m.frame.record));
      received.push(m);
    });
    // Also watch the spawn subject so we can correlate the ack (the dispatcher's reply).
    const acks: Message[] = [];
    await coordClient.subscribe(spawnSubject, (m) => {
      const rec = m.frame.record as { $type?: string };
      if (rec && rec.$type === "spawn.ack") {
        acks.push(m);
        log("spawn.ack:", JSON.stringify(m.frame.record));
      }
    });

    // Provision a REAL linked git worktree as the run's workdir (TASK-256 shape).
    const repoDir = join(bus.store, "src-repo");
    mkdirSync(repoDir, { recursive: true });
    run("git", ["init", "-q", "-b", "main", repoDir]);
    run("git", ["-C", repoDir, "-c", "user.email=a@b.c", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "init"]);
    const workdir = join(bus.store, "run-worktrees", runID);
    mkdirSync(join(bus.store, "run-worktrees"), { recursive: true });
    run("git", ["-C", repoDir, "worktree", "add", "-q", "-b", `sxrun/${runID}`, workdir]);
    log("provisioned linked worktree", workdir);

    // The dispatcher's child-creds dir.
    const childDir = join(bus.store, "children");
    mkdirSync(childDir, { recursive: true });

    // Launch the REAL dispatcher: --on-behalf mint, the embedded recipe as --harness
    // (run via `sh <recipe>`), watching the spawn subject. Its environment carries
    // SEXTANT_PI_EXTENSION (the bundle), the API key, the sandbox mode. By DEFAULT we
    // leave SEXTANT_PI_LOG UNSET so the recipe defaults it to ${SESSION_DIR}/pi-bus.log
    // — the EXACT live managed config (the live dispatcher plist sets no SEXTANT_PI_LOG).
    // The recipe's SESSION_DIR is $(dirname $SEXTANT_STORE)/pi-sessions/$CHILD_ID. Set
    // SEXTANT_STEPDONE_PILOG_IN_WORKDIR=1 to pin it inside the workdir instead.
    const piLogInWorkdir = !!process.env["SEXTANT_STEPDONE_PILOG_IN_WORKDIR"];
    const piLog = piLogInWorkdir ? join(workdir, "pi-bus.log") : ""; // "" → recipe default
    const dispEnv: NodeJS.ProcessEnv = {
      ...process.env,
      SEXTANT_PI_EXTENSION: BUNDLE,
      SX_PI_SANDBOX_MODE: process.env["REPRO_SANDBOX_MODE"] ?? "automode",
      ...(piLog ? { SEXTANT_PI_LOG: piLog } : {}),
      SX_PI_BIN: process.env["SX_PI_BIN"] ?? "pi",
    };
    // The recipe's default SESSION_DIR is $(dirname $SEXTANT_STORE)/pi-sessions/$CHILD_ID;
    // the dispatcher mints the child, so we learn its id from the spawn.ack (set below).
    let sessionDir = "";
    let defaultPiLog = "";
    const disp: ChildProcess = spawn(
      dispatchBin,
      [
        "--creds", dispatcher.credsPath,
        "--store", bus.store,
        "--on-behalf",
        "--harness", `sh ${RECIPE}`,
        "--subject", spawnSubject,
        "--workdir", childDir,
      ],
      { env: dispEnv, stdio: ["ignore", "pipe", "pipe"], cwd: REPO },
    );
    cleanups.push(() => disp.kill("SIGKILL"));
    disp.stdout?.on("data", (d: Buffer) => process.stderr.write(`[disp-out] ${d}`));
    disp.stderr?.on("data", (d: Buffer) => process.stderr.write(`[disp-err] ${d}`));
    await delay(1500); // let the dispatcher connect + subscribe

    // The coordinator-style work-step prompt (coordinator.workPrompt shape).
    const artifactName = "plan.canon-we";
    const prompt = [
      // The EXACT live prompt shape that hung (run 01KWD67G, dispatcher.log) — note
      // "Draft a plan first (you will pause for operator approval)", which makes the
      // model END ITS TURN AWAITING INPUT after creating the plan artifact, rather
      // than completing. SEXTANT_STEPDONE_SIMPLE_PROMPT=1 reverts to the plain shape.
      ...(process.env["SEXTANT_STEPDONE_SIMPLE_PROMPT"]
        ? [
            `Write and improve the work-engine canon document. Your deliverable is a durable bus artifact named "${artifactName}" (a document). Create it with sextant_artifact_put.`,
            "",
            "Draft the plan",
          ]
        : [
            `Author the work-engine canon. Draft a plan first (you will pause for operator approval), then edit. Your plan deliverable is a durable bus artifact named "${artifactName}" (a document); create it with sextant_artifact_put.`,
            "",
            "Draft the plan",
          ]),
      `RUN_EVENTS=${runEventsSubject} RUN_STEP=${stepID}`,
    ].join("\n");

    // Publish the spawn.request EXACTLY as coordinator.runDispatch does: prompt, the
    // step label as nickname, the run id as job, the per-run worktree as workdir, and
    // the per-step MODEL (RunStep.Model → SpawnRequest.Model). The live failing step
    // declares claude-opus-4-8 (a 1M-context model); the dispatcher reads THIS field
    // and sets SX_AGENT_MODEL for the recipe. SEXTANT_PI_MODEL alone does NOT reach the
    // worker on the managed path (the dispatcher overrides it from the request), which
    // is why an earlier run silently fell back to the default haiku.
    const stepModel = process.env["SEXTANT_PI_MODEL"] ?? "claude-opus-4-8";
    const spawnReq = {
      $type: "spawn.request",
      prompt,
      nickname: "Draft the plan",
      job: runID,
      model: stepModel,
      workdir,
    };
    log("spawn.request model =", stepModel);
    log("publishing spawn.request to", spawnSubject);
    await coordClient.publish(spawnSubject, spawnReq as unknown as Parameters<typeof coordClient.publish>[1]);

    // Learn the dispatcher-minted worker id from the spawn.ack, then compute the
    // recipe's default SESSION_DIR/pi-bus.log so we can read the trace it lands.
    {
      const ackDeadline = Date.now() + 20_000;
      while (Date.now() < ackDeadline && acks.length === 0) await delay(200);
      const workerId = (acks[0]?.frame.record as { id?: string } | undefined)?.id;
      if (workerId) {
        sessionDir = join(bus.store, "..", "pi-sessions", workerId);
        defaultPiLog = join(sessionDir, "pi-bus.log");
        log("worker id", workerId, "→ recipe default trace at", defaultPiLog);
      }
    }

    // Route an OPERATOR STEER to the worker's inbox WHILE it is running, exactly as
    // the live coordinator did (workflow.log:254 — applySteers publishes a chat.message
    // to msg.client.<worker>). This is the one live ingredient missing so far: it
    // buffers a wake that the worker's agent_end `next` branch (index.ts:302) delivers,
    // changing the terminal turn shape. Skip with SEXTANT_STEPDONE_NO_STEER=1.
    if (!process.env["SEXTANT_STEPDONE_NO_STEER"]) {
      const ackId = (acks[0]?.frame.record as { id?: string } | undefined)?.id;
      // Wait briefly for the ack so we know the worker id, then steer it mid-turn.
      const steerDeadline = Date.now() + 15_000;
      while (Date.now() < steerDeadline && acks.length === 0) await delay(200);
      const workerId = (acks[0]?.frame.record as { id?: string } | undefined)?.id ?? ackId;
      if (workerId) {
        await delay(3000); // let the worker get into its first turn (busy → buffers)
        const steer = { $type: "chat.message", text: "OPERATOR STEER for this run (incorporate it into your current task): Run spawned: Author the work-engine canon on rc/work-engine. CANON-ONLY: edit ADRs + docs/adr/README.md + CONTEXT.md." };
        log("routing steer to worker inbox msg.client." + workerId);
        await coordClient.publish(`msg.client.${workerId}`, steer as unknown as Parameters<typeof coordClient.publish>[1]);
      } else {
        log("WARN: no worker id from ack; skipping steer");
      }
    }

    // Wait for the step-done run.event (bounded — fail loud). Generous so a long opus
    // turn (esp. the steer-triggered second agent loop) has time to FINISH and reach
    // its terminal agent_end — distinguishing a genuine park (no report ever) from a
    // merely-slow turn. Override with SEXTANT_STEPDONE_WAIT_MS.
    const waitMs = Number(process.env["SEXTANT_STEPDONE_WAIT_MS"] ?? 600_000);
    const deadline = Date.now() + waitMs;
    while (Date.now() < deadline && received.length === 0) await delay(500);

    let artifactExists = false;
    try {
      await coordClient.getArtifact(artifactName);
      artifactExists = true;
    } catch {
      artifactExists = false;
    }

    log("=========================================");
    log("spawn.ack received:", acks.length > 0);
    log("artifact created on bus:", artifactExists);
    log("run.event(s) on run-events subject:", received.length);
    log(received.length > 0 ? "RESULT: PASS — step-done delivered on the DISPATCHED path" : "RESULT: FAIL — NO step-done (live defect reproduced on the dispatcher path)");
    log("=========================================");
    // Read the trace from wherever it actually landed: the workdir override if pinned,
    // else the recipe default ${SESSION_DIR}/pi-bus.log, else the EXTENSION default
    // <workdir>/.pi-bus.log (the recipe-independent fallback that survives a stale recipe).
    const extDefaultPiLog = join(workdir, ".pi-bus.log");
    const traceCandidates = piLog ? [piLog] : [defaultPiLog, extDefaultPiLog];
    let traceRead = false;
    for (const tracePath of traceCandidates) {
      try {
        const trace = readFileSync(tracePath, "utf8").trim().split("\n");
        traceRead = true;
        log("--- pi-bus trace from", tracePath, "---");
        for (const l of trace) {
          if (/run_step|auto_drain|agent_end|session_shutdown|run_event|handoff|connected|inbound|buffered|deferred/.test(l)) {
            console.log("  ", l);
          }
        }
      } catch {
        /* try next candidate */
      }
    }
    if (!traceRead) {
      log("!!! NO pi-bus.log landed — this IS the live symptom. Looked at:", traceCandidates.join(", "));
      log("    SESSION_DIR the recipe should use:", sessionDir);
    }
    process.exitCode = received.length > 0 ? 0 : 1;
  } finally {
    for (const c of cleanups.reverse()) {
      try {
        c();
      } catch {
        /* best effort */
      }
    }
    await delay(500);
  }
}

main().catch((e) => {
  console.error("[dispatched] error:", e);
  process.exit(2);
});
