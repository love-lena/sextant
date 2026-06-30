// captureWorktreeDiff — the live git side of the D7 code-step robustness fix. A
// coding step's natural deliverable is a git diff in the worker's worktree, not a
// bus artifact. So when a run step's worker reports done, if its workdir is a git
// repo with uncommitted changes, we deterministically snapshot the diff as a bus
// artifact and include it in the reported set — so a step that genuinely changed
// files passes the coordinator's proof gate with its real diff as proof, even if
// the model never created a bus artifact of its own.
//
// This is the WORKER reading its OWN worktree (its scoped CWD, SEXTANT_PI_WORKDIR)
// — never the opaque bus core. The coordinator still decides from typed metadata
// only (it gets a ref: name/kind/version), so single-writer + content-opacity hold.
//
// Scope discipline: a NO-OP unless the workdir is a git repo WITH changes. A truly
// hollow step (no bus artifact, no file changes) still reports 0 artifacts and is
// still — correctly — blocked. Best-effort: any git/create failure returns
// undefined (the report goes out with whatever the model produced), never throws.

import { execFile } from "node:child_process";
import { promisify } from "node:util";
import type { JSONValue } from "@sextant/sdk";
import type { ProducedArtifact } from "./run_report.js";

const exec = promisify(execFile);

// CreateArtifactFn creates a bus artifact from an opaque record, returning its
// initial revision (the SDK client's createArtifact, resolved live).
export type CreateArtifactFn = (name: string, record: JSONValue) => Promise<number>;

// A captured diff artifact is named so the coordinator and the dash can recognise
// it as a code-step deliverable without baking a lexicon into the bus: the run
// step id keys it (one diff per step), and the kind marks it a worktree diff.
const DIFF_KIND = "work.diff";

// gitChanges runs git in the workdir and returns the porcelain status + the full
// diff (tracked changes plus untracked files, so a NEW file the worker wrote counts
// as a change), or undefined if the workdir is not a git repo. Untracked files are
// included via `git diff` with --no-index would be heavy; instead we add them to the
// index intent (-N) read-only is not possible without mutating, so we capture the
// porcelain status (which lists untracked) AND `git diff HEAD` for content. The
// status is the load-bearing "did anything change" signal; the diff is the proof.
async function gitChanges(workdir: string): Promise<{ status: string; diff: string } | undefined> {
  try {
    // --is-inside-work-tree throws (non-zero) when workdir is not a git repo.
    await exec("git", ["-C", workdir, "rev-parse", "--is-inside-work-tree"]);
  } catch {
    return undefined; // not a git repo — nothing to capture
  }
  let status = "";
  try {
    const r = await exec("git", ["-C", workdir, "status", "--porcelain"], { maxBuffer: 8 * 1024 * 1024 });
    status = r.stdout;
  } catch {
    return undefined;
  }
  if (status.trim() === "") return undefined; // no changes — the no-op case
  let diff = "";
  try {
    // `git diff HEAD` shows tracked modifications + deletions against the last
    // commit. Untracked NEW files are not in a diff against HEAD, but they appear
    // in the porcelain status (as "??"), so the artifact records both: the status
    // is the authoritative change list; the diff is the human-legible proof.
    const r = await exec("git", ["-C", workdir, "diff", "HEAD"], { maxBuffer: 16 * 1024 * 1024 });
    diff = r.stdout;
  } catch {
    // A repo with no commits yet (no HEAD) can't diff against HEAD; fall back to a
    // plain `git diff` of the index. The status still proves the change set.
    try {
      const r = await exec("git", ["-C", workdir, "diff"], { maxBuffer: 16 * 1024 * 1024 });
      diff = r.stdout;
    } catch {
      diff = "";
    }
  }
  return { status, diff };
}

// makeCaptureWorktreeDiff builds the DiffCapture the RunReporter calls at report
// time. It captures the worker's worktree diff as a bus artifact named for the run
// step and returns a ref, or undefined when there is nothing to capture (no git
// repo, no changes, or no workdir configured). artifactName keys the artifact on
// the step so a re-dispatch overwrites rather than orphans (create-or-noop: if the
// name already exists the create fails and we return undefined, since the model's
// own artifact — or a prior diff — already stands as the deliverable).
export function makeCaptureWorktreeDiff(deps: {
  workdir: string;
  runStep: string;
  selfId: () => string;
  createArtifact: CreateArtifactFn;
  log: (event: string, fields?: Record<string, unknown>) => void;
}): () => Promise<ProducedArtifact | undefined> {
  return async () => {
    if (!deps.workdir) return undefined;
    const changes = await gitChanges(deps.workdir);
    if (!changes) return undefined; // not a git repo / no changes — no-op

    // Name the diff for the step so it is a stable, single deliverable per step.
    const name = `work.diff.${deps.runStep || "step"}`;
    const record = {
      $type: DIFF_KIND,
      kind: DIFF_KIND,
      step: deps.runStep,
      by: deps.selfId(),
      status: changes.status,
      diff: changes.diff,
    } as unknown as JSONValue;
    try {
      const version = await deps.createArtifact(name, record);
      deps.log("worktree_diff_artifact", { name, version });
      return { name, kind: DIFF_KIND, version };
    } catch (e) {
      // The name may already exist (the model created its own deliverable, or a
      // prior attempt captured a diff). That is fine — we don't clobber; the
      // existing artifact stands. Returning undefined leaves the reported set as
      // the model's own produced artifacts.
      deps.log("worktree_diff_create_skipped", { name, detail: (e as Error).message });
      return undefined;
    }
  };
}
