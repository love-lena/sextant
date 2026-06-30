#!/usr/bin/env bash
# Build the dash D2 web UI (ADR-0049 layout). The SPA source — JSX components,
# the HTML shell, styles, the vendored React/marked/purify, and the bus-bundle
# scope — is the web-dash client (clients/web-dash). The Go dash server
# (clients/sextant-dash) embeds the BUILT app, but go:embed cannot reach across
# `..`, so this script assembles the embeddable artifact INSIDE the server
# package: it transpiles the JSX, copies the static assets, and bundles the bus
# IIFE into clients/sextant-dash/dashapi/web/app/ — an all-generated, gitignored
# directory consumed by the go:embed in clients/sextant-dash/dashapi/debug.go.
#
# Run via `make ui`, `go generate ./...`, or directly; CI + scripts/release.sh
# run it before any Go compile (a build that skipped it fails to COMPILE).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/clients/web-dash"                         # SPA source (committed)
OUT="$ROOT/clients/sextant-dash/dashapi/web/app"     # embed target (generated, gitignored)
ESBUILD=(npx --yes esbuild@0.21.5)

mkdir -p "$OUT/vendor"

# 1. Transpile the JSX components to plain JS (classic transform → global React),
# so the served app needs no in-browser Babel and no runtime CDN.
for f in status assistant-brain artifact home sidebar artifacts review conversations goals bus mobilize workflow composer review-author workengine app; do
  "${ESBUILD[@]}" "$SRC/$f.jsx" \
    --jsx=transform \
    --jsx-factory=React.createElement --jsx-fragment=React.Fragment \
    --outfile="$OUT/$f.js" --log-level=warning
done
echo "transpiled dash UI components → $OUT/*.js"

# 2. Copy the static assets the SPA serves verbatim (the HTML shell, styles, the
# favicon, the image-slot helper, and the vendored libraries).
cp "$SRC/index.html" "$SRC/styles.css" "$SRC/favicon.png" "$SRC/image-slot.js" "$OUT/"
cp "$SRC"/vendor/*.min.js "$OUT/vendor/"
echo "copied static assets → $OUT/"

# 3. The bus bundle (ADR-0044): bundle @sextant/sdk (browser entry),
# @sextant/conv-goals, @sextant/conv-review and nats.ws into vendor/sextant-bus.js
# as a single IIFE that assigns window.SextantBus, so the classic-script SPA runs
# the conventions directly over its own bus Client over wss — no runtime CDN, no
# in-browser Babel (the ADR-0034 property holds). @sextant/sdk resolves to the
# node-free BROWSER entry via --conditions=browser. The guarded node-builtin
# fallbacks in nats.ws are dead in a browser, so they are marked external.
#
# Build the three bundled TS packages FROM CLEAN, in dependency order (the SDK
# first — the conventions depend on it via file:). esbuild bundles their BUILT
# outputs (dist/src/*.js), so this must not assume a pre-built dist/: a fresh
# checkout (CI, a fresh worktree) has none. `npm ci` when a lockfile is present
# (reproducible, CI-faithful), else `npm install`; then `tsc`.
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
build_ts_pkg "$ROOT/conventions/review/ts"
build_ts_pkg "$ROOT/conventions/workflow/ts"
build_ts_pkg "$ROOT/conventions/spawn/ts"
echo "built the bundled TS packages (sdk, conv-goals, conv-review, conv-workflow, conv-spawn) from clean"

# The bundle scope is the dependency root the three packages are linked into;
# install so the symlinks resolve to the just-built dist/.
BUNDLE="$SRC/bundle"
( cd "$BUNDLE" && npm install --no-audit --no-fund >/dev/null )
"${ESBUILD[@]}" "$BUNDLE/bus-entry.js" \
  --bundle --format=iife --platform=browser --target=es2020 \
  --conditions=browser \
  --external:crypto --external:util --external:fs --external:fs/promises \
  --external:stream --external:stream/web --external:perf_hooks \
  --outfile="$OUT/vendor/sextant-bus.js" --log-level=warning
echo "built dash bus bundle → $OUT/vendor/sextant-bus.js"

# 4. Build stamp (TASK-140): write build.json with this build's short SHA + UTC
# timestamp. The served dash polls /build.json and shows a quiet "new version
# available" nudge when the loaded build's SHA differs from what is now served.
# Generated like the *.js (gitignored), it rides into the go:embed via the
# trailing all:web/app line, so the embedded release dash carries a fixed stamp
# and simply never mismatches. Fall back to the timestamp if git is unavailable.
SHA="$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || true)"
BUILT_AT="$(date -u +%FT%TZ)"
if [ -z "$SHA" ]; then
  SHA="$BUILT_AT"
fi
printf '{"sha":"%s","builtAt":"%s"}\n' "$SHA" "$BUILT_AT" > "$OUT/build.json"
echo "wrote build stamp → $OUT/build.json (sha=$SHA)"
