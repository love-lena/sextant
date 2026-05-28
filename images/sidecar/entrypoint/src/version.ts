/**
 * Single source of truth for the sidecar's self-reported version.
 *
 * The version is read from the package manifest at runtime rather than
 * hand-written, so the `version` command and the MCP client-identity
 * handshake can never drift from `package.json` (or from each other).
 * Both `src/version.ts` and the built `dist/version.js` sit one level
 * under the package root, so `../package.json` resolves the same in the
 * compiled container, in `tsx`, and under vitest.
 *
 * createRequire is used instead of a static `import ... with { type:
 * "json" }` because the manifest lives outside tsconfig's `rootDir`
 * (`./src`); a runtime require sidesteps the "file is not under rootDir"
 * build error while keeping a single source of truth.
 */
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const pkg = require("../package.json") as { version: string };

/** Semver of `@sextant/sidecar`, sourced from package.json. */
export const SIDECAR_VERSION: string = pkg.version;
