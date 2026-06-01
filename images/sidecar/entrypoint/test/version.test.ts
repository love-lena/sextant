/**
 * Unit test for `src/version.ts::SIDECAR_VERSION`.
 *
 * Regresses slug:bug-sidecar-version-string-stale: the
 * `version` command and the MCP client-identity handshake used to
 * hard-code their version string, which drifted (the `version` command
 * reported 0.2.0 while package.json — and the MCP handshake — said
 * 0.1.0). SIDECAR_VERSION now reads package.json at runtime so the two
 * call sites share one source of truth. This test pins that invariant:
 * the exported constant must equal the manifest version, so a future
 * package.json bump can't silently leave a stale hard-coded string
 * behind.
 */

import { createRequire } from "node:module";
import { describe, expect, it } from "vitest";
import { SIDECAR_VERSION } from "../src/version.js";

const require = createRequire(import.meta.url);
const pkg = require("../package.json") as { version: string };

describe("SIDECAR_VERSION", () => {
  it("matches the package.json version (no drift)", () => {
    expect(SIDECAR_VERSION).toBe(pkg.version);
  });

  it("is a non-empty semver-shaped string", () => {
    expect(SIDECAR_VERSION).toMatch(/^\d+\.\d+\.\d+/);
  });
});
