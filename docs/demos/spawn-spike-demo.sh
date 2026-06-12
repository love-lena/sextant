#!/usr/bin/env bash
# M5.1 spawn-spike PoC — self-validating demo (TASK-70).
#
# Proves a dispatcher can launch agents that join a sextant bus under their own
# identity and run a task. Runs entirely on a THROWAWAY bus (fresh store + port);
# `--bare --strict-mcp-config` + a pinned $SEXTANT_STORE keep every spawn OFF the
# operator's live bus. Each spawn costs model tokens (one nested agent turn).
#
#   docs/demos/spawn-spike-demo.sh           # build, then run all slices
#   SX=/path/to/sextant SXMCP=/path/to/sextant-mcp docs/demos/spawn-spike-demo.sh
#
# Design notes: docs/demos/spawn-spike-notes.md
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
P="${P:-/tmp/spawn-spike}"; S="$P/store"; PORT="${PORT:-4480}"
SX="${SX:-$P/sextant}"; SXMCP="${SXMCP:-$P/sextant-mcp}"
PASS=0; FAIL=0
ok(){ echo "  PASS: $1"; PASS=$((PASS+1)); }
no(){ echo "  FAIL: $1"; FAIL=$((FAIL+1)); }
need(){ command -v "$1" >/dev/null 2>&1 || { echo "missing: $1"; exit 2; }; }
need claude; need codex

rm -rf "$P"; mkdir -p "$S"
echo "== build binaries =="
( cd "$ROOT" && go build -o "$SX" ./cmd/sextant && go build -o "$SXMCP" ./cmd/sextant-mcp ) || { echo "build failed"; exit 2; }

echo "== throwaway bus on :$PORT =="
"$SX" up --store "$S" --port "$PORT" >"$P/up.log" 2>&1 & BUS=$!
trap 'kill $BUS 2>/dev/null' EXIT
for _ in $(seq 1 100); do [ -f "$S/bus.json" ] && break; sleep 0.1; done
[ -f "$S/bus.json" ] || { echo "bus didn't start"; exit 2; }
"$SX" clients register dispatcher --kind agent --store "$S" --out "$P/disp.creds" >/dev/null 2>&1
reads(){ "$SX" read "$1" --since 0 --store "$S" --creds "$P/disp.creds" 2>&1; }
lists(){ "$SX" clients list --store "$S" --creds "$P/disp.creds" 2>&1; }

mcp_store(){ printf '{"mcpServers":{"sextant":{"command":"%s","env":{"SEXTANT_STORE":"%s"}}}}' "$SXMCP" "$S"; }
mcp_creds(){ printf '{"mcpServers":{"sextant":{"command":"%s","env":{"SEXTANT_CREDS":"%s","SEXTANT_STORE":"%s"}}}}' "$SXMCP" "$1" "$S"; }

echo "== AC#1: claude -p auto-mint (keyed identity) =="
echo "$(mcp_store)" >"$P/mcp1.json"
claude -p "You are a sextant bus worker. Using ONLY the sextant MCP tools, publish a chat.message with text 'hello A' on subject msg.topic.demo, then stop." \
  --bare --strict-mcp-config --mcp-config "$P/mcp1.json" --permission-mode bypassPermissions \
  --output-format json </dev/null >"$P/a.json" 2>"$P/a.err"
reads msg.topic.demo | grep -q "hello A" && ok "claude -p joined + published (AC#1)" || no "claude -p hello A not on bus"
lists | grep -q "claude-" && ok "auto-minted agent present in registry" || no "no auto-minted agent"

echo "== AC#4: claude -p as pre-registered nickname (dispatcher-known id) =="
"$SX" clients register vega --kind agent --store "$S" --out "$P/vega.creds" >/dev/null 2>&1
VEGA=$(lists | awk '/ vega /{print $1}')
echo "$(mcp_creds "$P/vega.creds")" >"$P/mcp4.json"
claude -p "You are a sextant bus worker. Using ONLY the sextant MCP tools, publish a chat.message with text 'hello vega' on subject msg.topic.demo, then stop." \
  --bare --strict-mcp-config --mcp-config "$P/mcp4.json" --permission-mode bypassPermissions \
  --output-format json </dev/null >"$P/v.json" 2>"$P/v.err"
reads msg.topic.demo | grep -q "<$VEGA>.*hello vega" && ok "spawned agent published AS vega (nickname, known id) (AC#4)" || no "vega publish not under known id $VEGA"

echo "== AC#2: codex exec auto-mint =="
codex exec "You are a sextant bus worker. Using only the sextant MCP tools, publish a chat.message with text 'hello codex' on subject msg.topic.demo. Then stop." \
  -c "mcp_servers.sextant.command=\"$SXMCP\"" -c "mcp_servers.sextant.env={ SEXTANT_STORE = \"$S\" }" \
  --dangerously-bypass-approvals-and-sandbox --skip-git-repo-check --cd "$P" </dev/null >"$P/codex.log" 2>&1
reads msg.topic.demo | grep -q "hello codex" && ok "codex exec joined + published (AC#2)" || no "codex hello not on bus"

echo "== AC#3: wake loop (supervisor) =="
# TODO: cmd/spawn-poc supervisor (its own SDK client) — Connect, Subscribe to
# msg.client.<spawned-id>, re-invoke `claude -p --resume <session>` on inbound DM;
# assert the agent wakes + acks. See docs/demos/spawn-spike-notes.md (Wake loop).
echo "  PENDING: supervisor build (cmd/spawn-poc) — AC#3"

echo "== result: $PASS passed, $FAIL failed (AC#3 pending) =="
[ "$FAIL" -eq 0 ]
