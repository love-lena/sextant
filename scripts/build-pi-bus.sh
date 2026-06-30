#!/usr/bin/env bash
# Build the pi-bus extension bundle (ADR-0052: pi is the work engine's sole
# harness). esbuild bundles the pi-bus extension (clients/pi-bus/src/index.ts)
# and its file: deps (@sextant/sdk, @sextant/conv-goals) + typebox into ONE
# self-contained ESM file the Go build embeds into the sextant binary at
# clients/sextant-cli/internal/components/embed/pi-bus.bundle.mjs. A brew install
# ships no node_modules, so the managed dispatcher's pi worker loads THIS bundle
# as its --extension (SEXTANT_PI_EXTENSION). pi provides the pi host
# (@earendil-works/pi-coding-agent is a type-only import here), so it is marked
# external.
#
# Generated + gitignored, like the dash UI bundles (TASK-121): naming the bundle
# in the go:embed makes a build that skipped this step FAIL TO COMPILE rather
# than ship a dispatcher that cannot launch a worker. CI, `make build`/`test`,
# and scripts/release.sh all run it before any Go compile. Run via `make pi-ext`,
# `go generate ./...`, or directly.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/clients/sextant-cli/internal/components/embed/pi-bus.bundle.mjs"
ESBUILD=(npx --yes esbuild@0.21.5)

# Build the file: deps FROM CLEAN, in dependency order (the SDK first — conv-goals
# depends on it via file:). esbuild bundles their BUILT outputs (dist/src/*.js),
# so a fresh checkout (CI, a fresh worktree) must build them first. `npm ci` when
# a lockfile is present (reproducible, CI-faithful), else `npm install`.
build_ts_pkg() { # build_ts_pkg <dir>
  local d="$1"
  if [ -f "$d/package-lock.json" ]; then
    ( cd "$d" && npm ci --no-audit --no-fund >/dev/null )
  else
    ( cd "$d" && npm install --no-audit --no-fund >/dev/null )
  fi
  ( cd "$d" && npm run build >/dev/null )
}
build_ts_pkg "$ROOT/sdk/ts"
build_ts_pkg "$ROOT/conventions/goal/ts"

# Install pi-bus's own deps so esbuild resolves the file: links (to the just-built
# dist/) and typebox from clients/pi-bus/node_modules.
( cd "$ROOT/clients/pi-bus" && npm install --no-audit --no-fund >/dev/null )

mkdir -p "$(dirname "$OUT")"
# Bundle the extension entry to a single ESM file (.mjs so node always treats it
# as ESM, independent of any surrounding package). --platform=node marks node
# builtins external; pi provides @earendil-works/pi-coding-agent (a type-only
# import here, erased at compile), so mark it external too. The extension's
# `export default function sextantPiBus(pi)` entry is preserved.
#
# THE createRequire BANNER (load-bearing — a silent extension-load failure on the
# live managed path, TASK-42). In an ESM bundle esbuild replaces every RUNTIME
# `require(...)` with a __require shim that THROWS ("Dynamic require of X is not
# supported"). A dependency pulled in via @sextant/sdk for client identity —
# tweetnacl — does a runtime `require("crypto")` for its PRNG at module init. With
# no real `require` in scope that throw happens AT IMPORT, so the whole default
# export never loads: pi's `-e <bundle>` load fails and the worker boots with ONLY
# pi's built-in tools — no sextant_* bus tools, no agent_end hook, no step-done.
# It is SILENT under the srt sandbox (the worker's stderr, where pi logs the load
# failure, is dropped). The banner gives the ESM bundle a real `require` via
# createRequire(import.meta.url), so tweetnacl's require("crypto") resolves to node
# crypto and the extension loads. The smoke test below FAILS THE BUILD if the bundle
# ever stops importing again (the durable guard for this class — relates to TASK-43).
"${ESBUILD[@]}" "$ROOT/clients/pi-bus/src/index.ts" \
  --bundle --format=esm --platform=node --target=node22 \
  --external:@earendil-works/pi-coding-agent \
  --banner:js='import { createRequire as __pibusCreateRequire } from "module"; const require = __pibusCreateRequire(import.meta.url);' \
  --outfile="$OUT" --log-level=warning
echo "built pi-bus extension bundle → $OUT"

# SMOKE TEST (the durable regression guard, TASK-42/43): node-import the BUILT .mjs
# and assert its default export is a function. A bundle that throws at import — the
# crypto-require failure above, or any future runtime-require/init throw — FAILS the
# build here instead of silently shipping a worker with no bus tools. We assert
# against the DEPLOYED ARTIFACT (the .mjs), never the TS source, because that is the
# gate-the-prod-adapter gap this whole bug lived in. @earendil-works/pi-coding-agent
# is externalized + only a type import (erased), so the import needs no pi host.
node --input-type=module -e "
  import(process.argv[1]).then((m) => {
    if (typeof m.default !== 'function') {
      console.error('pi-bus bundle smoke test FAILED: default export is ' + typeof m.default + ', want function');
      process.exit(1);
    }
    console.error('pi-bus bundle smoke test OK: default export is a function');
  }).catch((e) => {
    console.error('pi-bus bundle smoke test FAILED: import threw: ' + e.message);
    process.exit(1);
  });
" "$OUT"
