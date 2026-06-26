// Resolve the repo root so the conformance test reads
// protocol/conformance/vectors/review/ from one well-known location — the SAME
// JSON files the Go suite replays (FORMAT.md, ADR-0041), never a copy. Walks up
// from this module's directory to the `go.mod` + `protocol/conformance` marker,
// with a SEXTANT_REPO_ROOT env override for CI or unusual layouts. Mirrors the
// goals convention's repoRoot helper — never a hardcoded `../../..` depth, which
// the ADR-0049 move proved brittle.

import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

export function repoRoot(): string {
  const override = process.env["SEXTANT_REPO_ROOT"];
  if (override) return override;
  let dir = dirname(fileURLToPath(import.meta.url));
  for (;;) {
    if (existsSync(join(dir, "go.mod")) && existsSync(join(dir, "protocol", "conformance"))) {
      return dir;
    }
    const parent = dirname(dir);
    if (parent === dir) {
      throw new Error(
        "could not find the repo root (no go.mod with protocol/conformance above this module); set SEXTANT_REPO_ROOT",
      );
    }
    dir = parent;
  }
}

// reviewVectorsDir is the directory holding the review op-transcript conformance
// vectors — the protocol-owned, language-neutral files.
export function reviewVectorsDir(): string {
  return join(repoRoot(), "protocol", "conformance", "vectors", "review");
}
