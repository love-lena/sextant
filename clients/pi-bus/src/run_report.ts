// The workflow-run step-done reporter (ADR-0048 + the D7 fix). A dispatched pi
// worker that is wired into a run step (SEXTANT_PI_RUN_EVENTS / SEXTANT_PI_RUN_STEP)
// must, when it FINISHES the step, publish ONE run.event{status:"done"} on the
// coordinator's run-events subject carrying the artifacts it produced — the signal
// the coordinator waits on and gates the step's proof on (a step that reports done
// with NO artifact is the hollow-deliverable case; the coordinator blocks it).
//
// WHY a dedicated module, and WHY it emits at the DRAIN, not the first agent_end
// (the D7 root cause):
//
//   `agent_end` fires ONCE PER AGENT RUN (each agent.prompt()/continue()), and it
//   is NOT reliably "the whole task is done": a retryable-error stop, an
//   auto-compaction, or a follow-up turn all produce an agent_end while the worker
//   intends to keep going (the extension-level AgentEndEvent does not even carry
//   willRetry, so the extension cannot tell an intermediate stop from the final
//   one). Emitting the step-done on the FIRST agent_end — and latching a
//   report-once guard there — captured `produced` BEFORE the artifact-creating
//   turns ran: the worker really created build.r306/review.r306, but the step-done
//   had already gone out with artifacts:0, and the coordinator's proof gate
//   (correctly) blocked the completed work.
//
//   The deterministic "this worker is genuinely done with the step" point is the
//   drain-and-revive wind-down (ADR-0045): the dispatched worker did its task, the
//   wake queue is empty, and it is about to relinquish + exit. Reporting THERE
//   captures the COMPLETE produced set across every run in the process, and only
//   fires when the step is truly finished. The report stays idempotent (report
//   once per process) and is awaited before the drain closes the bus.
//
// ROBUSTNESS FOR CODE STEPS. A coding step's natural deliverable is a git diff in
// the worker's worktree, not a bus artifact — so a step that genuinely changed
// files would still report artifacts:0 and be blocked. At report time, if the
// worker's workdir is a git repo with uncommitted changes, we deterministically
// capture the diff as a bus artifact and include it in the reported set. This is
// the WORKER reading its OWN worktree (not the opaque core), so it holds the
// single-writer + content-opacity invariants: the coordinator still decides from
// typed metadata only. Gated on the workdir being a git repo WITH changes — a
// no-op for a step that touched nothing (a truly hollow step still reports 0 and
// is still blocked, as it must be).

import type { JSONValue } from "@sextant/sdk";

// ProducedArtifact is a ref (name/kind/version) to an artifact the worker created
// or updated this step — the shape the coordinator attaches and gates on. Refs
// only; never the content (content-opacity).
export interface ProducedArtifact {
  name: string;
  kind: string;
  version: number;
}

// PublishFn publishes one opaque record on a subject (the live SDK client's
// publish, resolved at call time so it reaches the current client).
export type PublishFn = (subject: string, record: JSONValue) => Promise<void>;

// DiffCapture captures the worker's worktree diff as a bus artifact and returns a
// ref to it, or undefined if the workdir is not a git repo or has no changes (the
// no-op case). Injected so the reporter stays pure and testable; the extension
// supplies the live git + artifact-create implementation (see captureWorktreeDiff).
export type DiffCapture = () => Promise<ProducedArtifact | undefined>;

// RunReporterDeps is the seam the reporter drives, so its behaviour is testable
// without pi or a live bus.
export interface RunReporterDeps {
  // runEventsSubject is the coordinator's run-events subject; "" means this worker
  // is not a run step and the reporter is a no-op.
  runEventsSubject: string;
  // runStep is the step id this worker is executing.
  runStep: string;
  // selfId resolves the worker's own bus id at report time (for `by`). "" if there
  // is no live client — then the reporter cannot publish and stays a no-op.
  selfId: () => string;
  // publish sends the run.event. Resolved against the live client at call time.
  publish: PublishFn;
  // producedSnapshot returns the artifacts the worker has created/updated so far —
  // read at report time so it reflects EVERYTHING produced up to the drain, not a
  // value captured at an earlier agent_end.
  producedSnapshot: () => ProducedArtifact[];
  // captureDiff captures the worktree diff as a bus artifact (code-step robustness),
  // or returns undefined (not a git repo / no changes). Optional — absent ⇒ skipped.
  captureDiff?: DiffCapture;
  // log traces each step (the extension's JSONL trace seam).
  log: (event: string, fields?: Record<string, unknown>) => void;
}

// RunReporter emits the run-step-done event exactly once per process. The extension
// calls report() at the worker's true terminal point (the drain-and-revive
// wind-down), and ALSO defensively at session_shutdown, so a worker that exits
// without taking the auto-drain path still reports. report() is idempotent: the
// first call wins, later calls are no-ops.
export class RunReporter {
  private reported = false;

  constructor(private readonly deps: RunReporterDeps) {}

  // isRunStep is true when this worker is wired into a run step (so the extension
  // can skip the work entirely off a plain mobilize/revive).
  isRunStep(): boolean {
    return this.deps.runEventsSubject !== "";
  }

  // hasReported reports whether the step-done has already gone out (the extension
  // uses it only for tracing).
  hasReported(): boolean {
    return this.reported;
  }

  // report publishes the step-done run.event once, carrying the COMPLETE produced
  // set (snapshot read NOW) plus — for a code step — the captured worktree diff. It
  // is idempotent and best-effort: a publish failure is logged, never thrown into
  // the lifecycle handler. `reason` traces what drove the report (drain vs shutdown).
  async report(reason: string): Promise<void> {
    if (this.reported) {
      this.deps.log("run_step_done_skip", { reason, detail: "already reported" });
      return;
    }
    if (!this.isRunStep()) return; // not a run step — nothing to report
    const self = this.deps.selfId();
    if (!self) {
      // No live client to publish through. Do NOT latch reported — a later call
      // (e.g. at shutdown after a transient client gap) may still get through.
      this.deps.log("run_step_done_deferred", { reason, detail: "no live bus client" });
      return;
    }
    this.reported = true;

    // The complete produced set, read at report time. A code step's deliverable is
    // a git diff, not a bus artifact — capture it deterministically and include it,
    // so a step that changed files passes the proof gate with its real diff. The
    // capture is gated on the workdir being a git repo WITH changes; otherwise it
    // returns undefined and we report only what the model produced (which may be 0
    // for a truly hollow step — correctly still blocked by the coordinator).
    const produced = [...this.deps.producedSnapshot()];
    if (this.deps.captureDiff) {
      try {
        const diff = await this.deps.captureDiff();
        if (diff && !produced.some((a) => a.name === diff.name)) {
          produced.push(diff);
          this.deps.log("run_step_diff_captured", { name: diff.name, version: diff.version });
        }
      } catch (e) {
        // A diff-capture failure must not block the report — the model-created
        // artifacts (if any) still go out. Logged for diagnosis.
        this.deps.log("run_step_diff_error", { detail: (e as Error).message });
      }
    }

    this.deps.log("run_step_done", { subject: this.deps.runEventsSubject, step: this.deps.runStep, reason, artifacts: produced.length });
    try {
      await this.deps.publish(this.deps.runEventsSubject, {
        $type: "run.event",
        step: this.deps.runStep,
        status: "done",
        by: self,
        outcome: "done",
        artifacts: produced.map((a) => ({ name: a.name, kind: a.kind, version: a.version })),
      } as unknown as JSONValue);
    } catch (e) {
      this.deps.log("run_event_error", { detail: (e as Error).message });
    }
  }
}
