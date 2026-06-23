#!/usr/bin/env bash
# rc.sh — release-candidate builds you can run on the live setup.
#
# Driven by the /rc skill (the agent runs this; it is NOT a hand-run one-liner).
# It owns the deterministic, reversible mechanics — building the rc binaries,
# launching an ephemeral side-by-side dash, and swapping/rolling back the live
# brew symlinks against a recorded restore manifest. Anything destructive to a
# LIVE surface (restarting the managed dash/components) is left to the skill so
# the agent can warn first; this runner only ever touches a recorded, reversible
# symlink set plus its own rc dir.
set -euo pipefail

RC_ROOT="${RC_ROOT:-$HOME/.sextant-rc}"
RC_BIN="$RC_ROOT/bin"
MANIFEST="$RC_ROOT/restore.tsv"          # TSV: <name>\t<stock-target|absent>, one per rc binary
EPHEMERAL="$RC_ROOT/ephemeral.tsv"        # TSV: <pid>\t<port>\t<url>\t<ref>, one per running dev dash
BREW_BIN="$(dirname "$(command -v sextant)")"

mkdir -p "$RC_ROOT"

# apps emits "<app-dir> <binary-name>" for each sextant* binary the worktree builds.
apps() {
  local wt="$1" d
  for d in sextant dash tui dispatch mcp violet workflow; do
    [ -d "$wt/clients/go/apps/$d" ] || continue
    case "$d" in
      sextant) echo "sextant sextant" ;;
      *)       echo "$d sextant-$d" ;;
    esac
  done
}

build_all() {
  local wt="$1" dir bin
  echo "building rc binaries from $wt (make ui first)"
  ( cd "$wt" && make ui >/dev/null )
  mkdir -p "$RC_BIN"
  while read -r dir bin; do
    echo "  go build -> $bin"
    ( cd "$wt" && go build -o "$RC_BIN/$bin" "./clients/go/apps/$dir" )
  done < <(apps "$wt")
  echo "built: $(cd "$RC_BIN" && echo *)"
}

cmd_build() { build_all "$1"; }

# dash: build sextant-dash, launch it side-by-side on a free port against the LIVE
# bus, serving the worktree SPA from disk (--ui). Prints the URL. Never touches the
# managed dash or the installed binaries.
cmd_dash() {
  local wt="$1"
  local ref="${2:-$wt}"
  local uidir="$wt/clients/go/apps/internal/dashapi/web/app"
  ( cd "$wt" && make ui >/dev/null )
  go build -o "$RC_BIN/sextant-dash" "$wt/clients/go/apps/dash" 2>/dev/null \
    || ( cd "$wt" && go build -o "$RC_BIN/sextant-dash" ./clients/go/apps/dash )
  local statefile; statefile="$RC_ROOT/dash-$$.state"
  "$RC_BIN/sextant-dash" --port 0 --ui "$uidir" --state-file "$statefile" \
    >"$RC_ROOT/dash-$$.log" 2>&1 &
  local pid=$!
  local _; for _ in $(seq 1 30); do [ -f "$statefile" ] && break; sleep 0.3; done
  if [ ! -f "$statefile" ]; then
    echo "FAIL: dev dash did not come up — log:"; tail -5 "$RC_ROOT/dash-$$.log"; kill "$pid" 2>/dev/null || true; exit 1
  fi
  local url port
  url=$(sed -n 's/.*"url":"\([^"]*\)".*/\1/p' "$statefile")
  port=$(sed -n 's/.*"port":\([0-9]*\).*/\1/p' "$statefile")
  printf '%s\t%s\t%s\t%s\n' "$pid" "$port" "$url" "$ref" >> "$EPHEMERAL"
  echo "DEV dash on port $port (ref $ref, pid $pid)"
  echo "URL: $url"
}

# stop: kill ephemeral dev dashes (all, or one by port).
cmd_stop() {
  local want="${1:-}"
  [ -f "$EPHEMERAL" ] || { echo "no dev dashes tracked"; return 0; }
  local tmp; tmp=$(mktemp)
  local pid port url ref
  while IFS=$'\t' read -r pid port url ref; do
    if [ -n "$want" ] && [ "$want" != "$port" ]; then
      printf '%s\t%s\t%s\t%s\n' "$pid" "$port" "$url" "$ref" >> "$tmp"; continue
    fi
    if kill "$pid" 2>/dev/null; then echo "stopped dev dash pid $pid (port $port)"; else echo "pid $pid already gone (port $port)"; fi
  done < "$EPHEMERAL"
  mv "$tmp" "$EPHEMERAL"
  [ -s "$EPHEMERAL" ] || rm -f "$EPHEMERAL"
}

# swap: repoint the live brew sextant* symlinks at the rc binaries, after recording
# the exact stock state (once) so rollback is byte-faithful.
cmd_swap() {
  [ -d "$RC_BIN" ] && [ -n "$(ls -A "$RC_BIN" 2>/dev/null)" ] || { echo "no rc binaries — run: rc.sh build <worktree>"; exit 1; }
  if [ ! -f "$MANIFEST" ]; then
    : > "$MANIFEST"
    local b name link tgt
    for b in "$RC_BIN"/*; do
      name=$(basename "$b"); link="$BREW_BIN/$name"
      if [ -L "$link" ]; then tgt=$(readlink "$link"); else tgt="absent"; fi
      printf '%s\t%s\n' "$name" "$tgt" >> "$MANIFEST"
    done
    echo "recorded stock restore point ($(wc -l < "$MANIFEST" | tr -d ' ') binaries) -> $MANIFEST"
  else
    echo "restore point already recorded (still swapped from a prior /rc install) -> $MANIFEST"
  fi
  local b name
  for b in "$RC_BIN"/*; do
    name=$(basename "$b"); ln -sf "$b" "$BREW_BIN/$name"; echo "  $name -> $b"
  done
  echo "SWAPPED. live sextant* now resolve to the rc. roll back with: rc.sh rollback"
}

cmd_rollback() {
  [ -f "$MANIFEST" ] || { echo "not swapped (no restore manifest) — nothing to roll back"; return 0; }
  local name tgt link
  while IFS=$'\t' read -r name tgt; do
    link="$BREW_BIN/$name"
    if [ "$tgt" = "absent" ]; then rm -f "$link"; echo "  removed rc-only $name";
    else ln -sf "$tgt" "$link"; echo "  $name -> $tgt"; fi
  done < "$MANIFEST"
  rm -f "$MANIFEST"
  echo "ROLLED BACK to stock."
}

cmd_status() {
  echo "brew bin     : $BREW_BIN"
  echo "sextant link : $(readlink "$BREW_BIN/sextant" 2>/dev/null || echo '(not a symlink)')"
  echo "sextant ver  : $(sextant version 2>/dev/null | head -1 || echo '?')"
  if [ -f "$MANIFEST" ]; then echo "STATE        : SWAPPED to rc ($(wc -l < "$MANIFEST" | tr -d ' ') binaries; rollback available)"; else echo "STATE        : stock"; fi
  if [ -f "$EPHEMERAL" ]; then
    echo "dev dashes   :"
    local pid port url ref
    while IFS=$'\t' read -r pid port url ref; do
      if kill -0 "$pid" 2>/dev/null; then echo "  port $port  pid $pid  ref $ref  $url"; else echo "  port $port  (pid $pid dead)  ref $ref"; fi
    done < "$EPHEMERAL"
  else
    echo "dev dashes   : none"
  fi
}

case "${1:-}" in
  build)    cmd_build "$2" ;;
  dash)     cmd_dash "$2" "${3:-}" ;;
  stop)     cmd_stop "${2:-}" ;;
  swap)     cmd_swap ;;
  rollback) cmd_rollback ;;
  status)   cmd_status ;;
  *) echo "usage: rc.sh {build <wt>|dash <wt> [ref]|stop [port]|swap|rollback|status}"; exit 2 ;;
esac
