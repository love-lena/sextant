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
