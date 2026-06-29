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
"${ESBUILD[@]}" "$ROOT/clients/pi-bus/src/index.ts" \
  --bundle --format=esm --platform=node --target=node22 \
  --external:@earendil-works/pi-coding-agent \
  --outfile="$OUT" --log-level=warning
echo "built pi-bus extension bundle → $OUT"
