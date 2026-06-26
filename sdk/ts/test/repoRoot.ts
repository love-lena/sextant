// Resolve the repo root so the conformance test reads
// protocol/conformance/vectors/wire/ from one well-known location — the SAME
// JSON files the Go SDK replays (FORMAT.md), never a copy. Walks up from this
// module's directory to the `go.mod` marker (the repo is one Go module), with a
// SEXTANT_REPO_ROOT env override for CI or unusual layouts.

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

// wireVectorsDir is the directory holding the wire (frame-codec) conformance
// vectors.
export function wireVectorsDir(): string {
  return join(repoRoot(), "protocol", "conformance", "vectors", "wire");
}
