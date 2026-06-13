#!/usr/bin/env bash
# Build the dash D2 web UI: transpile the JSX components to plain JS with esbuild
# (classic transform → global React), so the served app needs no in-browser
# Babel and no runtime CDN. The vendored React/ReactDOM/marked live under
# web/app/vendor/. Re-run this whenever a *.jsx source changes; the *.js outputs
# are committed and embedded by the Go build (go:embed web/app).
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
