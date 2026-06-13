#!/usr/bin/env bash
# M5.4 workflow coordinator — LIVE variant (TASK-26).
#
# The same end-to-end composition as m5-workflow-demo.sh, but each workflow step
# stands up a REAL `claude -p` agent instead of a CLI stub: the coordinator
# dispatches the step → the M5.2 dispatcher mints a NAMED identity and launches
# claude -p under it (M5.1's proven recipe) → the agent does the task and reports
# the step done by publishing a workflow.event through the sextant MCP tools →
# the coordinator records it and walks on.
#
# Costs model tokens (one claude run per step). Runs entirely on a THROWAWAY bus;
# --bare --strict-mcp-config + a pinned $SEXTANT_STORE keep every agent OFF the
# operator's live bus.
#
#   docs/demos/m5-workflow-live-demo.sh
#
# Companion to the token-free docs/demos/m5-workflow-demo.sh. Notes: m5-workflow-notes.md
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
P="${P:-/tmp/m5-workflow-live}"; S="$P/store"; PORT="${PORT:-4496}"
SX="${SX:-$P/sextant}"; SXMCP="${SXMCP:-$P/sextant-mcp}"; SXDISP="${SXDISP:-$P/sextant-dispatch}"; SXWF="${SXWF:-$P/sextant-workflow}"
PASS=0; FAIL=0
ok(){ echo "  PASS: $1"; PASS=$((PASS+1)); }
no(){ echo "  FAIL: $1"; FAIL=$((FAIL+1)); }
need(){ command -v "$1" >/dev/null 2>&1 || { echo "missing: $1"; exit 2; }; }
need claude

rm -rf "$P"; mkdir -p "$S"
echo "== build binaries =="
( cd "$ROOT" && go build -o "$SX" ./cmd/sextant && go build -o "$SXMCP" ./cmd/sextant-mcp \
  && go build -o "$SXDISP" ./cmd/sextant-dispatch && go build -o "$SXWF" ./cmd/sextant-workflow ) || { echo "build failed"; exit 2; }

echo "== throwaway bus on :$PORT =="
"$SX" up --store "$S" --port "$PORT" >"$P/up.log" 2>&1 & BUS=$!
trap 'kill $DISP $WF 2>/dev/null; kill $BUS 2>/dev/null' EXIT; DISP=""; WF=""
for _ in $(seq 1 100); do [ -f "$S/bus.json" ] && break; sleep 0.1; done
[ -f "$S/bus.json" ] || { echo "bus didn't start"; exit 2; }
"$SX" clients register dispatcher --kind agent --store "$S" --out "$P/disp.creds" >/dev/null 2>&1
"$SX" clients register coordinator --kind agent --store "$S" --out "$P/coord.creds" >/dev/null 2>&1
reads(){ "$SX" read "$1" --since 0 --store "$S" --creds "$P/coord.creds" 2>/dev/null; }
lists(){ "$SX" clients list --store "$S" --creds "$P/coord.creds" 2>/dev/null; }
state(){ "$SX" artifact get "workflow.$1" --json --store "$S" --creds "$P/coord.creds" 2>/dev/null | tr -d ' \n'; }
waitfor(){ local pat="$1" cmd="$2" to="${3:-25}"; for _ in $(seq 1 $((to*3))); do eval "$cmd" | grep -q "$pat" && return 0; sleep 0.34; done; return 1; }

# LIVE harness: the dispatcher runs this per step with SEXTANT_CREDS (the child's
# minted creds), SEXTANT_STORE, and $SX_PROMPT in the environment. It builds an MCP
# config pointing sextant-mcp at those creds, turns the coordinator's WF_EVENTS/
# WF_STEP directive into an instruction, and runs a real claude -p that joins the
# bus as the minted identity and reports the step done.
cat >"$P/live-harness.sh" <<'EOF'
#!/usr/bin/env sh
MCP="$(mktemp)"
printf '{"mcpServers":{"sextant":{"command":"%s","env":{"SEXTANT_CREDS":"%s","SEXTANT_STORE":"%s"}}}}' \
  "$SEXTANT_MCP_BIN" "$SEXTANT_CREDS" "$SEXTANT_STORE" > "$MCP"
EV=$(printf '%s' "$SX_PROMPT" | sed -n 's/.*WF_EVENTS=\([^ ]*\).*/\1/p')
ST=$(printf '%s' "$SX_PROMPT" | sed -n 's/.*WF_STEP=\([^ ]*\).*/\1/p')
TASK=$(printf '%s' "$SX_PROMPT" | sed 's/[[:space:]]*WF_EVENTS=.*//')
PRIMER="You are sextant worker $SX_CHILD_NICK doing one workflow step: $TASK Your ONLY action: call the sextant message publish tool to publish this EXACT record on subject $EV -- {\"\$type\":\"workflow.event\",\"step\":\"$ST\",\"status\":\"done\"}. Call the tool immediately; do NOT first check whether it is available or list tools. If the call errors with 'No such tool available', the MCP server is still connecting, so wait a moment and call it again, up to 10 times. Do not use any other tool, do not explain -- just publish, then stop."
# Scoped + cheap: only the sextant publish tool is pre-allowed (no blanket
# bypass), and the model is haiku. --strict-mcp-config + --bare keep it isolated.
exec claude -p "$PRIMER" --model claude-haiku-4-5 --bare --strict-mcp-config --mcp-config "$MCP" \
  --permission-mode default --allowedTools "mcp__sextant__message_publish" \
  --output-format json </dev/null
EOF
chmod +x "$P/live-harness.sh"
export SEXTANT_MCP_BIN="$SXMCP"  # inherited by the dispatcher and on into each harness

echo "== start the M5.2 dispatcher (--on-behalf; live claude -p harness) =="
"$SXDISP" --creds "$P/disp.creds" --on-behalf --store "$S" --subject msg.topic.spawn \
  --harness "$P/live-harness.sh" --deadline 300s >"$P/disp.log" 2>&1 & DISP=$!
sleep 1

echo "== run a 2-step workflow that dispatches REAL claude agents =="
cat >"$P/plan.json" <<EOF
{"id":"wflive","steps":[
  {"id":"review","kind":"dispatch","nickname":"reviewer","prompt":"Acknowledge you are reviewing the change."},
  {"id":"merge","kind":"dispatch","nickname":"merger","prompt":"Acknowledge you are merging the change."}
]}
EOF
"$SXWF" --creds "$P/coord.creds" --store "$S" --plan "$P/plan.json" --id wflive \
  --spawn-subject msg.topic.spawn --step-timeout 200s >"$P/wf.log" 2>&1 & WF=$!

if waitfor 'workflow wflive: done' "cat $P/wf.log" 360; then
  ok "the workflow walked both steps to done driving LIVE claude agents (end-to-end)"
  lists | grep -qE "[[:space:]]reviewer[[:space:]]+agent[[:space:]]" && lists | grep -qE "[[:space:]]merger[[:space:]]+agent[[:space:]]" \
    && ok "each step stood up a REAL claude agent under its dispatcher-minted name (reviewer, merger)" \
    || no "named live agents not in the directory"
  reads msg.workflow.wflive.events | grep -q '"step":"review","status":"done"' \
    && reads msg.workflow.wflive.events | grep -q '"step":"merge","status":"done"' \
    && ok "the live agents reported their steps done on the workflow event stream" \
    || no "step-done events from the live agents missing"
  DONES=$(state wflive | grep -o '"status":"done"' | wc -l | tr -d ' ')
  [ "$DONES" -ge 3 ] && ok "state artifact checkpointed both steps + the workflow done (CAS)" || no "state not fully done (dones=$DONES)"
else
  no "workflow never reached done"; echo "--- coordinator ---"; tail -8 "$P/wf.log"; echo "--- dispatcher ---"; tail -15 "$P/disp.log"
fi
wait $WF 2>/dev/null; WF=""

echo "== result: $PASS passed, $FAIL failed =="
[ "$FAIL" -eq 0 ]
