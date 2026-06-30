// Unit tests for the live worktree-diff capture (the D7 code-step robustness fix),
// against REAL temp git repos. They prove:
//   - a git repo WITH uncommitted changes → a diff artifact is created + a ref
//     returned (a code step's deliverable passes the proof gate);
//   - a git repo with NO changes → undefined (no-op; a hollow step stays blocked);
//   - a non-git workdir → undefined (no-op);
//   - an empty workdir config → undefined (a non-coding/unscoped worker).

import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { JSONValue } from "@sextant/sdk";
import { makeCaptureWorktreeDiff } from "../src/worktree_diff.js";

function git(dir: string, ...args: string[]): void {
  execFileSync("git", ["-C", dir, ...args], { stdio: "pipe" });
}

// initRepo makes a temp git repo with one committed file, returning its path.
function initRepo(): string {
  const dir = mkdtempSync(join(tmpdir(), "pi-bus-diff-"));
  git(dir, "init", "-q");
  git(dir, "config", "user.email", "t@t");
  git(dir, "config", "user.name", "t");
  writeFileSync(join(dir, "a.txt"), "one\n");
  git(dir, "add", "-A");
  git(dir, "commit", "-q", "-m", "init");
  return dir;
}

// A recording createArtifact that captures the diff record.
function recordingCreate(): { create: (n: string, r: JSONValue) => Promise<number>; created: { name: string; record: Record<string, unknown> }[] } {
  const created: { name: string; record: Record<string, unknown> }[] = [];
  return {
    created,
    create: async (name, record) => {
      created.push({ name, record: record as Record<string, unknown> });
      return 1;
    },
  };
}

test("a git repo WITH uncommitted changes captures a diff artifact + returns a ref", async () => {
  const dir = initRepo();
  try {
    // Mutate a tracked file (the worker's edit).
    writeFileSync(join(dir, "a.txt"), "one\ntwo\n");
    const { create, created } = recordingCreate();
    const capture = makeCaptureWorktreeDiff({ workdir: dir, runStep: "s3", selfId: () => "w", createArtifact: create, log: () => {} });

    const ref = await capture();

    assert.ok(ref, "a change yields a ref");
    assert.equal(ref!.name, "work.diff.s3");
    assert.equal(ref!.kind, "work.diff");
    assert.equal(created.length, 1, "one diff artifact created");
    const rec = created[0].record;
    assert.equal(rec["step"], "s3");
    assert.match(String(rec["diff"]), /two/, "the diff records the change content");
    assert.match(String(rec["status"]), /a\.txt/, "the porcelain status lists the changed file");
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("a NEW untracked file is detected via the porcelain status (a created-file code step)", async () => {
  const dir = initRepo();
  try {
    writeFileSync(join(dir, "new.txt"), "fresh\n"); // untracked
    const { create, created } = recordingCreate();
    const capture = makeCaptureWorktreeDiff({ workdir: dir, runStep: "s1", selfId: () => "w", createArtifact: create, log: () => {} });
    const ref = await capture();
    assert.ok(ref, "an untracked new file counts as a change");
    assert.match(String(created[0].record["status"]), /new\.txt/, "status lists the untracked file");
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("a git repo with NO changes → undefined (no-op; a hollow step stays blocked)", async () => {
  const dir = initRepo();
  try {
    const { create, created } = recordingCreate();
    const capture = makeCaptureWorktreeDiff({ workdir: dir, runStep: "s1", selfId: () => "w", createArtifact: create, log: () => {} });
    const ref = await capture();
    assert.equal(ref, undefined, "no changes → no diff");
    assert.equal(created.length, 0, "no artifact created");
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("a non-git workdir → undefined (no-op)", async () => {
  const dir = mkdtempSync(join(tmpdir(), "pi-bus-nogit-"));
  try {
    writeFileSync(join(dir, "x.txt"), "loose\n");
    const { create } = recordingCreate();
    const capture = makeCaptureWorktreeDiff({ workdir: dir, runStep: "s1", selfId: () => "w", createArtifact: create, log: () => {} });
    const ref = await capture();
    assert.equal(ref, undefined, "not a git repo → no diff");
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("an empty workdir config → undefined (a non-coding / unscoped worker)", async () => {
  const { create } = recordingCreate();
  const capture = makeCaptureWorktreeDiff({ workdir: "", runStep: "s1", selfId: () => "w", createArtifact: create, log: () => {} });
  const ref = await capture();
  assert.equal(ref, undefined);
});

// D14 (TASK-265): the worker runs under the TASK-118 pi-auto sandbox, which
// deny-reads `~/.gitconfig`. Under that deny every `git` invocation exits 128
// ("unable to access '.../.gitconfig'"), so the capture used to return undefined
// → a real code step reported 0 artifacts → the proof gate blocked it. The fix
// forces GIT_CONFIG_GLOBAL=/dev/null + GIT_CONFIG_SYSTEM=/dev/null on every git
// call so the unreadable global config is never consulted.
//
// This test REPRODUCES the deny by pointing the surrounding process env's
// GIT_CONFIG_GLOBAL at a DIRECTORY (git errors reading a directory as a config
// file — the same exit-128 failure mode as a chmod-000 / sandbox-denied global).
// The fake-pass guard is the explicit sanity assertion: a bare `git status` run
// under exactly this env MUST fail; if it didn't, the test wouldn't exercise the
// deny and the fix's override would prove nothing. captureWorktreeDiff (with the
// fix) spreads process.env — carrying the broken GIT_CONFIG_GLOBAL — but
// overrides it to /dev/null, so it STILL captures the diff.
test("D14: an unreadable global git config does NOT defeat the capture (forces GIT_CONFIG_GLOBAL=/dev/null)", async () => {
  const dir = initRepo();
  // A directory makes git fail to read the global config (exit 128), reproducing
  // the sandbox's `~/.gitconfig` deny without depending on chmod semantics.
  const badCfg = join(dir, "unreadable-cfg-dir");
  mkdirSync(badCfg);
  writeFileSync(join(dir, "a.txt"), "one\ntwo\n"); // the worker's edit

  const savedGlobal = process.env.GIT_CONFIG_GLOBAL;
  const savedSystem = process.env.GIT_CONFIG_SYSTEM;
  process.env.GIT_CONFIG_GLOBAL = badCfg; // unreadable global (a directory)
  process.env.GIT_CONFIG_SYSTEM = "/dev/null";
  try {
    // Fake-pass guard / sanity: confirm we actually reproduced the deny — a plain
    // `git status` under exactly this env MUST fail (git can't read the global).
    let bareFailed = false;
    try {
      execFileSync("git", ["-C", dir, "status", "--porcelain"], { stdio: "pipe", env: process.env });
    } catch {
      bareFailed = true;
    }
    assert.ok(bareFailed, "sanity: a bare `git status` MUST fail under the unreadable global config — otherwise the test doesn't exercise the deny");

    // With the fix, capture still succeeds because it forces the global to /dev/null.
    const { create, created } = recordingCreate();
    const capture = makeCaptureWorktreeDiff({ workdir: dir, runStep: "s9", selfId: () => "w", createArtifact: create, log: () => {} });
    const ref = await capture();

    assert.ok(ref, "the diff is captured even though the global git config is unreadable");
    assert.equal(ref!.name, "work.diff.s9");
    assert.equal(created.length, 1, "one diff artifact created under the deny");
    assert.match(String(created[0].record["diff"]), /two/, "the diff records the change content despite the broken global config");
  } finally {
    if (savedGlobal === undefined) delete process.env.GIT_CONFIG_GLOBAL;
    else process.env.GIT_CONFIG_GLOBAL = savedGlobal;
    if (savedSystem === undefined) delete process.env.GIT_CONFIG_SYSTEM;
    else process.env.GIT_CONFIG_SYSTEM = savedSystem;
    rmSync(dir, { recursive: true, force: true });
  }
});
