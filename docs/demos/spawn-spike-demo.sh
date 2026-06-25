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
SX="${SX:-$P/sextant}"; SXMCP="${SXMCP:-$P/sextant-mcp}"; SXPOC="${SXPOC:-$P/spawn-poc}"
PASS=0; FAIL=0
ok(){ echo "  PASS: $1"; PASS=$((PASS+1)); }
no(){ echo "  FAIL: $1"; FAIL=$((FAIL+1)); }
need(){ command -v "$1" >/dev/null 2>&1 || { echo "missing: $1"; exit 2; }; }
need claude; need codex

rm -rf "$P"; mkdir -p "$S"
echo "== build binaries =="
( cd "$ROOT" && go build -o "$SX" ./clients/sextant-cli && go build -o "$SXMCP" ./clients/sextant-mcp && go build -o "$SXPOC" ./clients/go/apps/spawn-poc ) || { echo "build failed"; exit 2; }

echo "== throwaway bus on :$PORT =="
"$SX" up --store "$S" --port "$PORT" >"$P/up.log" 2>&1 & BUS=$!
trap 'kill $BUS 2>/dev/null' EXIT
for _ in $(seq 1 100); do [ -f "$S/bus.json" ] && break; sleep 0.1; done
[ -f "$S/bus.json" ] || { echo "bus didn't start"; exit 2; }
"$SX" clients register dispatcher --kind agent --store "$S" --out "$P/disp.creds" >/dev/null 2>&1
"$SX" clients register boss --kind human --store "$S" --out "$P/boss.creds" >/dev/null 2>&1  # a distinct DM sender for the wake loop
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
# Capture this agent's session + bus id; AC#3's live slice re-invokes THIS agent.
SESSION_A=$(sed -n 's/.*"session_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$P/a.json" | head -1)
AGENT_A=$(reads msg.topic.demo | awk -F'[<>]' '/hello A/{print $2; exit}')

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

echo "== AC#3a: wake-loop supervisor MECHANISM (its own SDK client; no model tokens) =="
# The supervisor (clients/go/apps/spawn-poc) connects as the dispatcher, watches a client's DM
# subject, and re-invokes --on-wake on each inbound message (from anyone but the
# agent/itself), threading the message text through $SX_WAKE_TEXT. Here --on-wake
# just publishes an ack via the CLI, so this proves Connect + DM-watch + re-invoke
# + text-passing + --once + fail-loud deadline deterministically, for free.
"$SX" clients register worker --kind agent --store "$S" --out "$P/worker.creds" >/dev/null 2>&1
WORKER=$(lists | awk '/ worker /{print $1}')
cat >"$P/wake-mech.sh" <<EOF
#!/usr/bin/env sh
exec "$SX" publish msg.topic.demo "{\"\\\$type\":\"chat.message\",\"text\":\"mech-ack: \$SX_WAKE_TEXT\"}" --store "$S" --creds "$P/disp.creds"
EOF
chmod +x "$P/wake-mech.sh"
"$SXPOC" --creds "$P/disp.creds" --store "$S" --agent "$WORKER" \
  --on-wake "$P/wake-mech.sh" --once --deadline 30s >"$P/sup-mech.log" 2>&1 & SUP=$!
sleep 1  # let it subscribe (DeliverAll also closes the start race)
"$SX" publish "msg.client.$WORKER" '{"$type":"chat.message","text":"ping"}' --store "$S" --creds "$P/boss.creds" >/dev/null 2>&1
wait $SUP 2>/dev/null
reads msg.topic.demo | grep -q "mech-ack: ping" \
  && ok "supervisor woke + re-invoked on inbound, threading the message text (AC#3 mechanism)" \
  || { no "supervisor mechanism: no mech-ack on bus"; cat "$P/sup-mech.log"; }

echo "== AC#3b: LIVE resume-wake (re-invoke the AC#1 agent via claude -p --resume) =="
# Wake the SAME one-shot agent from AC#1 by its resume-stable session: the woken
# agent rejoins under its SAME keyed bus id and acts. The wake adapter primes a
# retry because the re-launched MCP server can still be `pending` on the first turn.
if [ -n "$SESSION_A" ] && [ -n "$AGENT_A" ]; then
  cat >"$P/wake-live.sh" <<'EOF'
#!/usr/bin/env sh
PRIMER="You are a sextant bus worker, woken to handle one message. The sextant MCP tools may still be connecting: if a tool call returns 'No such tool available', call it again, retrying up to 8 times until it succeeds. Your task: "
exec claude -p "$PRIMER$SX_WAKE_TEXT" --resume "$SESSION" --bare --strict-mcp-config \
  --mcp-config "$MCP1" --permission-mode bypassPermissions --output-format json </dev/null
EOF
  chmod +x "$P/wake-live.sh"
  SESSION="$SESSION_A" MCP1="$P/mcp1.json" "$SXPOC" --creds "$P/disp.creds" --store "$S" \
    --agent "$AGENT_A" --on-wake "$P/wake-live.sh" --once --deadline 150s --wake-timeout 140s \
    >"$P/sup-live.log" 2>&1 & SUP=$!
  sleep 1
  "$SX" publish "msg.client.$AGENT_A" '{"$type":"chat.message","text":"publish a chat.message with text awake-ack on subject msg.topic.demo, then stop."}' \
    --store "$S" --creds "$P/boss.creds" >/dev/null 2>&1
  wait $SUP 2>/dev/null
  reads msg.topic.demo | grep -q "<$AGENT_A>.*awake-ack" \
    && ok "resumed agent woke + acted UNDER ITS SAME keyed id $AGENT_A (AC#3 live)" \
    || { no "live resume-wake: no awake-ack under $AGENT_A"; tail -2 "$P/sup-live.log"; }
else
  no "AC#3 live skipped: could not capture AC#1 session/agent id"
fi

echo "== result: $PASS passed, $FAIL failed =="
[ "$FAIL" -eq 0 ]
