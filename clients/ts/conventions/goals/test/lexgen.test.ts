// A drift guard for the generated record types: re-running lexgen over the lexicon
// must reproduce the committed src/goal_gen.ts byte-for-byte. This is the TS peer of
// the Go convention's generate discipline — the generated types cannot silently
// drift from protocol/lexicons/goal.json (ADR-0041), and a hand-edit of the
// generated file (or a lexicon change without `npm run generate`) fails loudly here.
//
// It runs the generator into a temp output, then compares to the committed file. It
// needs no bus; it runs everywhere.

import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, copyFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// The package dir (this compiled test lives in dist/test; the source tree is two
// levels up from there in the repo, but we resolve the package dir from the source
// layout: walk up to the dir holding tools/lexgen.ts).
function packageDir(): string {
  // dist/test/lexgen.test.js → dist → package root.
  return dirname(dirname(dirname(fileURLToPath(import.meta.url))));
}

test("the committed goal_gen.ts is exactly what lexgen produces (no drift)", () => {
  const pkg = packageDir();
  const committed = join(pkg, "src", "goal_gen.ts");
  const before = readFileSync(committed, "utf8");

  // Regenerate into a scratch copy: run lexgen, which writes src/goal_gen.ts in
  // place, then read it and restore the committed bytes so the test does not mutate
  // the working tree. (lexgen writes to a fixed path; we snapshot/restore.)
  const tmp = mkdtempSync(join(tmpdir(), "lexgen-drift-"));
  const backup = join(tmp, "goal_gen.ts.bak");
  copyFileSync(committed, backup);
  try {
    const r = spawnSync("node", [join(pkg, "tools", "lexgen.ts")], { cwd: pkg, encoding: "utf8" });
    assert.equal(r.status, 0, `lexgen failed: ${r.stderr}`);
    const regenerated = readFileSync(committed, "utf8");
    assert.equal(
      regenerated,
      before,
      "src/goal_gen.ts is out of date — run `npm run generate` after editing the lexicon (and never hand-edit the generated file)",
    );
  } finally {
    // Restore the committed bytes regardless of outcome.
    copyFileSync(backup, committed);
    rmSync(tmp, { recursive: true, force: true });
  }
});
