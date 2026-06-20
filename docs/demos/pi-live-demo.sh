#!/usr/bin/env bash
# The TASK-184 capstone: one command that proves the whole co-equal-clients refactor
# works end to end on the operator's machine — a co-equal TypeScript pi client, on
# its OWN scoped identity, wakes on a DM, replies over the bus, moves a goal that
# renders, and streams its thinking + tool-calls to a bus activity topic the dash
# renders live. The operator runs it and watches a headless pi worker like a crew
# member in the dash.
#
# It is FULLY HERMETIC and SELF-SUFFICIENT FROM CLEAN:
#   - Hermetic: it stands up a THROWAWAY bus with SEXTANT_HOME pinned to a temp store
#     on every CLI/agent/pi process, so it never touches the operator's real bus or
#     active context, and tears everything down at the end. The operator's live setup
#     is untouched (verify: `sextant context list` still shows `lena` active).
#   - From clean: it installs + builds the TS workspace (the @sextant/pi-bus package,
#     the SDK + goals deps) and builds the Go bus + dash UI from source — no
#     pre-built artifacts, no SEXTANT_REPO_ROOT / SEXTANT_BIN overrides assumed.
#
# REAL MODEL: the pi agent runs a real Anthropic model — needs ANTHROPIC_API_KEY in
# the environment (a few cents per run, expected).
#
# This is the script the /pi-live-demo slash-command skill runs. An operator can also
# run it directly:  bash docs/demos/pi-live-demo.sh
set -euo pipefail

REPO="$(cd "$(dirname "$0")/../.." && pwd)"
PI_DIR="$REPO/clients/ts/pi"

say() { printf '\033[1;36m[pi-live-demo]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[pi-live-demo] %s\033[0m\n' "$*" >&2; exit 1; }

# pi (the coding agent) is installed under the user's npm-global bin; make sure it is
# reachable (the demo launches `pi --mode rpc`).
export PATH="$HOME/.npm-global/bin:$PATH"

command -v npm >/dev/null 2>&1 || die "npm is not on PATH (the demo builds the TS workspace)."
command -v go  >/dev/null 2>&1 || die "the go toolchain is not on PATH (the demo builds + runs the real Go bus)."
command -v pi  >/dev/null 2>&1 || die "pi is not on PATH — install @earendil-works/pi-coding-agent (npm i -g), then re-run."
[ -n "${ANTHROPIC_API_KEY:-}" ] || die "ANTHROPIC_API_KEY is not set — the pi agent runs a real model. Export it and re-run."

# From-clean: link the workspace deps (file:../sdk, file:../conventions/goals) so the
# build resolves them on a fresh checkout. `npm run live-demo` then builds the deps,
# builds this package, and runs the self-validating driver.
say "installing the @sextant/pi-bus workspace (links the SDK + goals deps) — from clean"
( cd "$PI_DIR" && npm install --no-audit --no-fund >/dev/null )

say "building + running the live demo (this builds the Go bus + dash UI from source on first use)"
say "the driver prints PASS/FAIL per step, then keeps the dash up so you can watch + DM the pi worker"
echo
cd "$PI_DIR"
exec npm run --silent live-demo
