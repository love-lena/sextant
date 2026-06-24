#!/usr/bin/env bash
# Build the dash D2 web UI: transpile the JSX components to plain JS with esbuild
# (classic transform → global React), so the served app needs no in-browser
# Babel and no runtime CDN. The vendored React/ReactDOM/marked live under
# web/app/vendor/. The *.js outputs are GENERATED, not committed (TASK-121):
# they're gitignored and embedded by the Go build (go:embed in
# clients/go/apps/internal/dashapi/debug.go). Run via `make ui`, `go generate ./...`, or
# directly; CI + scripts/release.sh run it before any Go compile.
set -euo pipefail
DIR="$(cd "$(dirname "$0")/../clients/go/apps/internal/dashapi/web/app" && pwd)"
ESBUILD=(npx --yes esbuild@0.21.5)

for f in tweaks-panel artifact home sidebar artifacts review conversations goals mobilize workflow composer review-author app; do
  "${ESBUILD[@]}" "$DIR/$f.jsx" \
    --jsx=transform \
    --jsx-factory=React.createElement --jsx-fragment=React.Fragment \
    --outfile="$DIR/$f.js" --log-level=warning
done
echo "built dash UI components → $DIR/{tweaks-panel,artifact,home,sidebar,artifacts,review,conversations,goals,mobilize,workflow,composer,review-author,app}.js"

# The bus bundle (ADR-0044): bundle @sextant/sdk (browser entry), @sextant/conv-goals,
# @sextant/conv-review and nats.ws into vendor/sextant-bus.js as a single IIFE that
# assigns window.SextantBus, so the classic-script SPA runs the conventions directly
# over its own bus Client over wss — no runtime CDN, no in-browser Babel (the
# ADR-0034 property holds). @sextant/sdk resolves to the node-free BROWSER entry via
# --conditions=browser (the conventions' own @sextant/sdk imports redirect there
# too). The guarded node-builtin fallbacks in nats.ws (require('crypto') etc.) are
# dead in a browser, so the builtins are marked external rather than polyfilled.
TS="$(cd "$(dirname "$0")/../clients/ts" && pwd)"
BUNDLE="$(cd "$(dirname "$0")/../clients/go/apps/internal/dashapi/web/bundle" && pwd)"

# Build the three bundled TS packages FROM CLEAN, in dependency order (the SDK
# first — the conventions depend on it via file:). esbuild bundles their BUILT
# outputs (dist/src/*.js), so this must not assume a pre-built dist/: a fresh
# checkout (CI, a fresh worktree) has none, and the Go build runs this script
# before any compile, so it must be self-sufficient. `npm ci` when a lockfile is
# present (reproducible, CI-faithful), else `npm install`; then `tsc`. Done per
# package because each is its own npm project with a file: link to the prior one.
build_ts_pkg() { # build_ts_pkg <dir>
  local d="$1"
  if [ -f "$d/package-lock.json" ]; then
    ( cd "$d" && npm ci --no-audit --no-fund >/dev/null )
  else
    ( cd "$d" && npm install --no-audit --no-fund >/dev/null )
  fi
  ( cd "$d" && npm run build >/dev/null )
}
build_ts_pkg "$TS/sdk"
build_ts_pkg "$TS/conventions/goals"
build_ts_pkg "$TS/conventions/review"
echo "built the bundled TS packages (sdk, conv-goals, conv-review) from clean"

# The bundle scope (web/bundle) is the dependency root the three packages are
# linked into; install so the symlinks resolve to the just-built dist/.
( cd "$BUNDLE" && npm install --no-audit --no-fund >/dev/null )
"${ESBUILD[@]}" "$BUNDLE/bus-entry.js" \
  --bundle --format=iife --platform=browser --target=es2020 \
  --conditions=browser \
  --external:crypto --external:util --external:fs --external:fs/promises \
  --external:stream --external:stream/web --external:perf_hooks \
  --outfile="$DIR/vendor/sextant-bus.js" --log-level=warning
echo "built dash bus bundle → $DIR/vendor/sextant-bus.js"

# Build stamp (TASK-140): write web/app/build.json with this build's short SHA +
# UTC timestamp. The served dash polls /build.json; when the loaded build's SHA
# differs from what's now served it shows a quiet "new version available" nudge.
# Generated like the *.js (gitignored, not committed) but it rides into the
# go:embed via the trailing `all:web/app` line, so the embedded release dash
# also carries a (fixed) stamp and simply never mismatches. The SHA is the
# repo's short HEAD; if git is unavailable we fall back to a UTC timestamp so a
# stamp always exists (a missing build.json must not break the build).
SHA="$(git -C "$DIR" rev-parse --short HEAD 2>/dev/null || true)"
BUILT_AT="$(date -u +%FT%TZ)"
if [ -z "$SHA" ]; then
  SHA="$BUILT_AT"
fi
printf '{"sha":"%s","builtAt":"%s"}\n' "$SHA" "$BUILT_AT" > "$DIR/build.json"
echo "wrote build stamp → $DIR/build.json (sha=$SHA)"
