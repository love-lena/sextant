#!/usr/bin/env bash
# Build the release tarballs for a tag: the six binaries cross-compiled per
# platform plus the Claude Code plugin directory, so one unpacked tarball is a
# complete, versioned install (TASK-47). CI runs this from the release
# workflow; running it locally produces the same dist/ layout for inspection.
#
#   scripts/release.sh v0.1.0
#
# Output: dist/sextant_<tag>_<os>_<arch>.tar.gz, each containing
#   bin/{sextant,sextant-dash,sextant-mcp,sextant-dispatch,sextant-violet,sextant-workflow}
#   clients/claude-code/   (the plugin: manifest, marketplace, skill, .mcp.json)
set -euo pipefail
cd "$(dirname "$0")/.."

tag="${1:?usage: scripts/release.sh <tag>}"
platforms=(darwin/arm64 darwin/amd64 linux/amd64 linux/arm64)
ldflags="-s -w -X github.com/love-lena/sextant/internal/version.Version=${tag}"

# Generate the dash UI bundles (.jsx -> .js, TASK-121). They're generated, not
# committed, so the go:embed in internal/dashapi needs them present before we
# cross-compile below. Platform-independent JS, so build once up front.
bash scripts/build-dash-ui.sh

rm -rf dist
for p in "${platforms[@]}"; do
  os="${p%/*}" arch="${p#*/}"
  name="sextant_${tag}_${os}_${arch}"
  out="dist/${name}"
  mkdir -p "${out}/bin"
  for cmd in sextant sextant-dash sextant-mcp sextant-dispatch sextant-violet sextant-workflow; do
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
      go build -trimpath -ldflags "$ldflags" -o "${out}/bin/${cmd}" "./cmd/${cmd}"
  done
  mkdir -p "${out}/clients"
  cp -R clients/claude-code "${out}/clients/claude-code"
  # The plugin manifest version tracks the release, not whatever the repo
  # copy last said.
  manifest="${out}/clients/claude-code/.claude-plugin/plugin.json"
  python3 - "$manifest" "${tag#v}" <<'EOF'
import json, sys
path, ver = sys.argv[1], sys.argv[2]
m = json.load(open(path))
m["version"] = ver
json.dump(m, open(path, "w"), indent=2)
EOF
  tar -czf "dist/${name}.tar.gz" -C dist "$name"
  rm -rf "$out"
  echo "built dist/${name}.tar.gz"
done
