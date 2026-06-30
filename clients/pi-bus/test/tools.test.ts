// Unit tests for the worker-callable bus tools — specifically sextant_run_block (D8),
// the tool a VERIFY worker calls to STOP the run when the Definition of Done is not met.
//
// The properties under test:
//   - sextant_run_block is registered ONLY when the worker is a run step; a non-run
//     worker (plain mobilize/revive) never gets a tool that could block a run it isn't on.
//   - invoking it drives the RunReporter to a blocked outcome with the supplied reason —
//     the real worker→outcome path the coordinator's runVerify gate keys on.

import { test } from "node:test";
import assert from "node:assert/strict";
import { registerTools, type ToolDeps } from "../src/tools.js";
import { RunReporter } from "../src/run_report.js";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

// runReporter builds a reporter over the D7 deps seam; runEventsSubject "" ⇒ not a run step.
function runReporter(runEventsSubject: string): RunReporter {
  return new RunReporter({
    runEventsSubject,
    runStep: runEventsSubject === "" ? "" : "verify",
    selfId: () => "w",
    publish: async () => {},
    producedSnapshot: () => [],
    log: () => {},
  });
}

// A tiny pi stub that captures every registerTool call so a test can find a tool by name
// and invoke its execute(). Mirrors the fakePi pattern in gate.test.ts.
interface RegisteredTool {
  name: string;
  execute: (id: string, params: Record<string, unknown>) => Promise<{ isError: boolean; content: { text: string }[] }>;
}
function fakePi(): { pi: ExtensionAPI; tools: Map<string, RegisteredTool> } {
  const tools = new Map<string, RegisteredTool>();
  const pi = {
    registerTool(t: RegisteredTool) {
      tools.set(t.name, t);
    },
  } as unknown as ExtensionAPI;
  return { pi, tools };
}

// deps builds a ToolDeps with the given reporter and a no-op client resolver (the run_block
// tool needs neither a client nor the wake/subscription machinery).
function deps(reporter: RunReporter | undefined): ToolDeps {
  return {
    getClient: () => undefined,
    onWake: () => {},
    subscriptions: new Map(),
    runReporter: reporter,
  };
}

test("sextant_run_block is NOT registered off a run", () => {
  const { pi, tools } = fakePi();
  registerTools(pi, deps(runReporter(""))); // empty events subject ⇒ not a run step
  assert.equal(tools.has("sextant_run_block"), false, "a non-run worker must not get the block tool");
});

test("sextant_run_block is NOT registered when no reporter is supplied", () => {
  const { pi, tools } = fakePi();
  registerTools(pi, deps(undefined));
  assert.equal(tools.has("sextant_run_block"), false);
});

test("sextant_run_block is registered on a run step and latches the reporter blocked", async () => {
  const { pi, tools } = fakePi();
  const reporter = runReporter("msg.workflow.run.RUN1.events");
  registerTools(pi, deps(reporter));

  const tool = tools.get("sextant_run_block");
  assert.ok(tool, "the block tool is registered on a run step");
  assert.equal(reporter.isBlocked(), false, "not blocked until called");

  const res = await tool!.execute("call-1", { reason: "go build ./... failed" });
  assert.equal(res.isError, false, "the tool succeeds");
  // The real worker→outcome path: invoking the tool drove the reporter to blocked with the
  // exact reason — so the step-done run.event will carry outcome:blocked the coordinator gates on.
  assert.equal(reporter.isBlocked(), true, "invoking the tool latched the run blocked");
  assert.equal(reporter.reason(), "go build ./... failed", "the supplied reason is stored");
});
