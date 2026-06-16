#!/usr/bin/env bash
# Build the dash D2 web UI: transpile the JSX components to plain JS with esbuild
# (classic transform → global React), so the served app needs no in-browser
# Babel and no runtime CDN. The vendored React/ReactDOM/marked live under
# web/app/vendor/. The *.js outputs are GENERATED, not committed (TASK-121):
# they're gitignored and embedded by the Go build (go:embed in
# internal/dashapi/debug.go). Run via `make ui`, `go generate ./...`, or
# directly; CI + scripts/release.sh run it before any Go compile.
set -euo pipefail
DIR="$(cd "$(dirname "$0")/../internal/dashapi/web/app" && pwd)"
ESBUILD=(npx --yes esbuild@0.21.5)

for f in tweaks-panel artifact home sidebar app; do
  "${ESBUILD[@]}" "$DIR/$f.jsx" \
    --jsx=transform \
    --jsx-factory=React.createElement --jsx-fragment=React.Fragment \
    --outfile="$DIR/$f.js" --log-level=warning
done
echo "built dash UI components → $DIR/{tweaks-panel,artifact,home,sidebar,app}.js"

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
