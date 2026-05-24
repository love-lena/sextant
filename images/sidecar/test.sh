#!/usr/bin/env bash
# Sextant sidecar image — M9 acceptance smoke.
#
# Acceptance from `plans/bootstrap.md#M9`: "image builds; `docker run`
# starts an interactive shell with all the tools present."
#
# Steps:
#   1. Build the image via `make sidecar-image`.
#   2. Run a shell inside the image and check every spec-listed tool
#      is on PATH.
#   3. Print the image size and the major language versions.
#   4. Confirm the sidecar entrypoint's dist/index.js is in place.
#
# Plan: plans/bootstrap.md#M9
# Spec: specs/components/sidecar-image.md

set -euo pipefail

# OrbStack on macOS puts the docker CLI here. CI runs on ubuntu-24.04
# which has docker on the default PATH, so this prepend is a harmless
# no-op there.
export PATH="${HOME}/.orbstack/bin:${PATH}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${REPO_ROOT}"

red() { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
section() { printf '\n=== %s ===\n' "$*"; }

if ! command -v docker >/dev/null; then
  red "X docker CLI not on PATH (need OrbStack or Docker installed)"
  exit 1
fi

section "build sidecar image"
make sidecar-image

IMAGE="sextant-sidecar:latest"

section "verify required tools are present"
docker run --rm "${IMAGE}" /bin/bash -c '
  set -e
  for t in node npm git gh jq yq rg fzf curl wget make gcc python3 go vim; do
    if ! command -v "$t" >/dev/null; then
      echo "missing: $t" >&2
      exit 1
    fi
  done
  echo OK
'

section "language versions"
docker run --rm "${IMAGE}" /bin/bash -c '
  set -e
  node --version
  go version
  python3 --version
  npm --version
'

section "sidecar entrypoint built"
docker run --rm "${IMAGE}" /bin/bash -c '
  set -e
  ls /opt/sextant/sidecar/dist/index.js
  node /opt/sextant/sidecar/dist/index.js --version
'

section "image size"
SIZE_BYTES=$(docker image inspect "${IMAGE}" --format='{{.Size}}')
SIZE_MB=$(( SIZE_BYTES / 1024 / 1024 ))
echo "${IMAGE}: ${SIZE_MB} MiB"
if (( SIZE_MB > 3072 )); then
  red "! image is ${SIZE_MB} MiB (>3 GiB) — spec target is <2 GiB; investigate"
fi

green "OK — sidecar image smoke passed"
