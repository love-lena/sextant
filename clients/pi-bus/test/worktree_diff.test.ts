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
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
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
