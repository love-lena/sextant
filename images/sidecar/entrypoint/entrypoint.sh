#!/usr/bin/env bash
# Sextant sidecar entrypoint.
#
# Plan: plans/bootstrap.md#M9
# Spec: specs/components/sidecar-image.md §"Sidecar entrypoint"
#
# Used by sextantd at agent spawn time (M11+) to start the sidecar
# runtime in long-running mode. For M9 the container's default CMD is
# `/bin/bash` (so the smoke test can verify the tool set); this script
# is the path sextantd will set as CMD/entrypoint when spawning agents.
#
# Refuses to start unless the required env vars are set so misconfig
# fails fast instead of producing a half-connected sidecar.

set -euo pipefail

require_env() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "sidecar: env var ${name} is required (set by sextantd at spawn time)" >&2
    exit 1
  fi
}

require_env SEXTANT_AGENT_UUID
require_env SEXTANT_AGENT_NAME
require_env SEXTANT_HOST_ID
require_env SEXTANT_NATS_URL

exec node /opt/sextant/sidecar/dist/index.js run
