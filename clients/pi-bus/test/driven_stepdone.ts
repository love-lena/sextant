// The managed step-done regression harness — the end-to-end proof of the
// MANAGED work-engine publish path the unit tests cannot reach. It stands up a
// real throwaway bus, builds the real `sextant` binary, and launches a REAL
// `pi --mode rpc` worker via the ACTUAL embedded dispatcher recipe (pi.sh) +
// the ACTUAL embedded esbuild bundle (what `sextant components start dispatcher`
// materializes), wired exactly as a workflow RUN STEP: the prompt carries the
// trailing "RUN_EVENTS=<subject> RUN_STEP=<id>" directive the way
// coordinator.workPrompt appends it, SEXTANT_PI_DRAIN_WHEN_IDLE=1, the worker's
// own scoped creds. The worker creates a bus artifact and finishes; we subscribe
// to the run-events subject (exactly what coordinator.awaitStepDone awaits) and
// assert a run.event{status:"done"} lands carrying that artifact.
//
// WHY this harness exists. The "managed work step never completes" defect lived
// on the publish boundary every unit skips: the pi-bus unit tests drive
// RunReporter with deps ALREADY populated, so a broken prompt→env lift (pi.sh),
// a stale embedded bundle, or a recipe drift would still pass them while the live
// managed worker never publishes its step-done and the coordinator hangs. This
// drives the real recipe + real bundle + a real pi turn, so that whole chain is
// exercised once, for real. (The cheap, model-FREE half of the same guard lives
// in the Go components package, TestRecipeLiftsRunStepEnv, and runs in CI.)
//
// Runs in BOTH sandbox postures: default `automode` (skips the srt install), or
// REPRO_SANDBOX_MODE=sandbox to exercise the production srt hard wall (proves the
// loopback NATS run.event publish survives the network jail). Like the other
// driven.* harnesses it needs ANTHROPIC_API_KEY + the Go toolchain and is NOT a
// CI test (it costs a few model cents).
//
// Run: npm run driven:stepdone

import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import { readFileSync, mkdirSync } from "node:fs";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { connect, type Client, type Message } from "@sextant/sdk";
import { startBus, goAvailable, repoRoot } from "./busharness.js";

// run executes a command synchronously and returns stdout, throwing on non-zero exit
// (the worktree provisioning must fail loud — a fake/plain dir would mask the bug).
function run(cmd: string, args: string[]): string {
  const r = spawnSync(cmd, args, { encoding: "utf8" });
  if (r.status !== 0) {
    throw new Error(`${cmd} ${args.join(" ")} failed (${r.status}): ${r.stdout}\n${r.stderr}`);
  }
  return r.stdout ?? "";
}

// repoRoot walks up to the Go module root, so the embedded-asset paths resolve
// the same whether this runs compiled (dist/test) or via tsx (test/).
const REPO = repoRoot();
// Use the EMBEDDED recipe (what `sextant components start dispatcher` ships).
const RECIPE = join(REPO, "clients", "sextant-cli", "internal", "components", "embed", "pi.sh");
// The REAL managed path loads the embedded esbuild bundle (what `sextant
// components start dispatcher` materializes), not dist/src/index.js. Test that.
const EXTENSION =
  process.env["SEXTANT_PI_EXTENSION"] ??
  join(REPO, "clients", "sextant-cli", "internal", "components", "embed", "pi-bus.bundle.mjs");
const MODEL = process.env["SEXTANT_PI_MODEL"] ?? "claude-haiku-4-5";

function log(...a: unknown[]) {
  console.log("[stepdone]", ...a);
}

async function main() {
  if (!goAvailable()) throw new Error("Go toolchain required");
  if (!process.env["ANTHROPIC_API_KEY"]) throw new Error("ANTHROPIC_API_KEY required");

  const bus = startBus();
  const cleanups: Array<() => void> = [() => bus.stop()];
  try {
    // The "coordinator" identity that awaits the step-done.
    const coord = bus.mint("coordinator", "agent");
    // The child worker identity (the dispatcher mints this; here we mint directly).
    const child = bus.mint("writer", "agent");

    const coordClient: Client = await connect({ credsPath: coord.credsPath, connInfoPath: join(bus.store, "bus.json") });
    cleanups.push(() => void coordClient.close());

    // The run-events subject the coordinator subscribes to and the worker must publish to.
    const runID = "01KWREPRO0000000000000000";
    const stepID = "n1reprostep";
    const runEventsSubject = `msg.workflow.run.${runID}.events`;

    const received: Message[] = [];
    await coordClient.subscribe(runEventsSubject, (m) => {
      log("EVENT on run-events:", JSON.stringify(m.frame.record));
      received.push(m);
    });

    // The prompt EXACTLY as coordinator.workPrompt builds it (clients/coordinator/main.go:716-741):
    // "<Objective>\n\n<Label>" + the trailing "RUN_EVENTS=<subject> RUN_STEP=<id>"
    // directive line. NO "create artifact X" instruction and NO "you are done" — a real
    // work step gives the model the objective + label and lets it decide. The artificial
    // "do not do anything else" in the earlier version made the model stop CLEANLY (agent
    // loop ends → agent_end fires), which MASKED the live failure mode (the worker ending
    // its turn awaiting-input so agent_end never fires). This is the faithful shape.
    const artifactName = "plan.canon-we";
    const prompt = [
      "Write and improve the work-engine canon document. Your deliverable is a durable bus artifact named " +
        `"${artifactName}" (a document). Create it with sextant_artifact_put.`,
      "",
      "Draft the plan",
      `RUN_EVENTS=${runEventsSubject} RUN_STEP=${stepID}`,
    ].join("\n");

    // Provision a REAL linked git worktree as the worker's workdir — exactly what the
    // coordinator's per-run provisioning (TASK-256, clients/coordinator/worktree.go) does:
    // a `git worktree add` whose gitdir + commondir live in the SOURCE repo, OUTSIDE the
    // worktree. This is the element the earlier harness missed (it used a plain scratch
    // dir), and it is the variable that landed in the regression window
    // (01KWA9ZJ→01KWD2D7): D7's captureDiff (run_report.ts:160-172) runs `git` in this
    // worktree during report(), and a linked worktree under the srt sandbox is the
    // untested interaction. Set SEXTANT_STEPDONE_PLAIN_WORKDIR=1 to fall back to a plain
    // dir (the old, passing-but-unfaithful config) for an A/B.
    const repoDir = join(bus.store, "src-repo");
    let workdir: string;
    if (process.env["SEXTANT_STEPDONE_PLAIN_WORKDIR"]) {
      workdir = join(bus.store, "wd");
      mkdirSync(workdir, { recursive: true });
    } else {
      mkdirSync(repoDir, { recursive: true });
      run("git", ["init", "-q", "-b", "main", repoDir]);
      run("git", ["-C", repoDir, "-c", "user.email=a@b.c", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "init"]);
      workdir = join(bus.store, "run-worktrees", runID);
      mkdirSync(join(bus.store, "run-worktrees"), { recursive: true });
      run("git", ["-C", repoDir, "worktree", "add", "-q", "-b", `sxrun/${runID}`, workdir]);
      log("provisioned linked worktree", workdir, "gitdir →", run("git", ["-C", workdir, "rev-parse", "--absolute-git-dir"]).trim());
    }

    // Launch the REAL recipe. The env mirrors the dispatcher's launchHarness() exactly.
    const env: NodeJS.ProcessEnv = {
      ...process.env,
      SEXTANT_CREDS: child.credsPath,
      SEXTANT_STORE: bus.store,
      SEXTANT_PI_EXTENSION: EXTENSION,
      SX_PROMPT: prompt,
      SX_CHILD_ID: child.id,
      SX_CHILD_NICK: "writer",
      SX_JOB: runID,
      SX_AGENT_MODEL: MODEL,
      SEXTANT_PI_WORKDIR: workdir,
      SX_PI_SANDBOX_MODE: process.env["REPRO_SANDBOX_MODE"] ?? "automode", // default automode (skip srt); set sandbox to test the real hard wall
      SEXTANT_PI_LOG: join(bus.store, "pi-bus.log"),
      SX_PI_BIN: process.env["SX_PI_BIN"] ?? "pi",
    };

    log("launching pi worker via recipe", RECIPE, "model", MODEL);
    const proc: ChildProcess = spawn("sh", [RECIPE], { env, stdio: ["ignore", "pipe", "pipe"] });
    cleanups.push(() => proc.kill("SIGKILL"));
    proc.stdout?.on("data", (d: Buffer) => process.stderr.write(`[pi-out] ${d}`));
    proc.stderr?.on("data", (d: Buffer) => process.stderr.write(`[pi-err] ${d}`));

    // Wait for the step-done run.event (bounded — fail loud).
    const deadline = Date.now() + 120_000;
    while (Date.now() < deadline && received.length === 0) {
      await delay(500);
    }

    // Independently check the artifact actually got created (proves the turn ran + bus works).
    let artifactExists = false;
    try {
      await coordClient.getArtifact(artifactName);
      artifactExists = true;
    } catch {
      artifactExists = false;
    }

    log("=========================================");
    log("artifact created on bus:", artifactExists);
    log("run.event(s) on run-events subject:", received.length);
    if (received.length > 0) {
      log("RESULT: PASS — step-done delivered:", JSON.stringify(received[0].frame.record));
    } else {
      log("RESULT: FAIL — NO step-done run.event (defect reproduced)");
    }
    log("=========================================");
    // Dump the pi-bus trace tail so we can see whether report/isRunStep fired.
    try {
      const trace = readFileSync(join(bus.store, "pi-bus.log"), "utf8");
      const lines = trace.trim().split("\n");
      log("--- pi-bus trace (run_step / drain / report lines) ---");
      for (const l of lines) {
        if (/run_step|auto_drain|session_shutdown|run_event|isRunStep|connected|handoff/.test(l)) {
          console.log("  ", l);
        }
      }
    } catch (e) {
      log("(no pi-bus trace)", (e as Error).message);
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
    // give processes a moment to die
    await delay(500);
  }
}

main().catch((e) => {
  console.error("[stepdone] error:", e);
  process.exit(2);
});
