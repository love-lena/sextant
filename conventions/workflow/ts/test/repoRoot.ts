// Resolve the repo root so the conformance test reads
// protocol/conformance/vectors/workflow/ from one well-known location — the SAME JSON
// files the Go suite replays (FORMAT.md, ADR-0041), never a copy. Walks up from this
// module's directory to the `go.mod` + `protocol/conformance` marker, with a
// SEXTANT_REPO_ROOT env override — never a hardcoded `../../..` depth.

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

// workflowVectorsDir is the directory holding the workflow op-transcript conformance
// vectors — the protocol-owned, language-neutral files.
export function workflowVectorsDir(): string {
  return join(repoRoot(), "protocol", "conformance", "vectors", "workflow");
}
