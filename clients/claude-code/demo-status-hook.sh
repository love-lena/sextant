#!/usr/bin/env bash
# One-command, self-validating demo of the per-agent status hook (TASK-87 + the
# TASK-84 agent.status primitive): the plugin's PostToolUse hook keeps an agent's
# status current by calling Haiku to classify {state, headline} from recent
# activity, on a throttle, and writes it to the agent's own `status.<self>`
# artifact on the bus. Hermetic — a local mock stands in for the Anthropic API
# (SEXTANT_STATUS_API_BASE) and a throwaway bus holds the artifact.
#
#   clients/claude-code/demo-status-hook.sh
#
# It drives the GENUINE `sextant-mcp status` binary exactly as the hook would and
# proves:
#   1. GATING   — no per-session identity (a regular non-bus session) ⇒ skip.
#   2. FIRE     — connected ⇒ the detached worker calls (mock) Haiku and upserts
#                 the `status.<self>` artifact {state, headline} on the bus.
#   3. THROTTLE — an immediate second fire is throttled (no second Haiku call).
set -uo pipefail

REPO="$(cd "$(dirname "$0")/../.." && pwd)"
D="$(mktemp -d /tmp/status-hook-demo.XXXXXX)"
BIN="$D/bin"; PDATA="$D/plugin-data"; STORE="$D/store"; SESS="status-demo-session"
say()  { printf '\033[1;36m[demo]\033[0m %s\n' "$*"; }
ulid() { grep -oE '[0-9A-HJKMNP-TV-Z]{26}' | head -1; }
pass=0 fail=0
check() { if [ "$2" -eq 0 ]; then printf '  \033[1;32m[PASS]\033[0m %s\n' "$1"; pass=$((pass+1)); else printf '  \033[1;31m[FAIL]\033[0m %s\n' "$1"; fail=$((fail+1)); fi; }

MOCK_PID=""; BUS_PID=""
trap 'kill "$MOCK_PID" "$BUS_PID" 2>/dev/null || true; rm -rf "$D"' EXIT

mkdir -p "$BIN" "$PDATA/attest-identity" "$STORE"
say "building sextant + sextant-mcp from $REPO"
(cd "$REPO" && go build -o "$BIN/sextant" ./cmd/sextant && go build -o "$BIN/sextant-mcp" ./cmd/sextant-mcp)

say "starting a mock Anthropic endpoint (no real API call)"
cat > "$D/mock.py" <<'PY'
import http.server, json, socketserver
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        self.rfile.read(int(self.headers.get('content-length', 0)))
        body = json.dumps({"content": [{"type": "text", "text": "working | implementing the status hook prototype"}]}).encode()
        self.send_response(200); self.send_header('content-type', 'application/json')
        self.send_header('content-length', str(len(body))); self.end_headers(); self.wfile.write(body)
    def log_message(self, *a): pass
srv = socketserver.TCPServer(("127.0.0.1", 0), H)
print(srv.server_address[1], flush=True); srv.serve_forever()
PY
python3 "$D/mock.py" > "$D/port.txt" 2>/dev/null & MOCK_PID=$!
for _ in $(seq 1 50); do [ -s "$D/port.txt" ] && break; sleep 0.1; done
APIBASE="http://127.0.0.1:$(cat "$D/port.txt")"

say "starting a throwaway bus + registering the worker identity"
"$BIN/sextant" up --store "$STORE" --port 0 >"$D/bus.log" 2>&1 & BUS_PID=$!
for _ in $(seq 1 100); do [ -f "$STORE/bus.json" ] && break; sleep 0.1; done
[ -f "$STORE/bus.json" ] || { say "bus did not start"; cat "$D/bus.log"; exit 1; }
WORKER_ID="$("$BIN/sextant" clients register worker --store "$STORE" | ulid)"
WORKER_CREDS="$STORE/worker.creds"
ART="status.$WORKER_ID"

# A synthetic transcript: recent activity the worker digests for Haiku.
cat > "$D/transcript.jsonl" <<'JSON'
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"build the status hook prototype"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"writing the throttle + worker"},{"type":"tool_use","name":"Write","input":{}}]}}
JSON

HOOK_STDIN="{\"session_id\":\"$SESS\",\"transcript_path\":\"$D/transcript.jsonl\",\"cwd\":\"$D\",\"hook_event_name\":\"PostToolUse\"}"
run_hook() { # drives sextant-mcp status as the PostToolUse hook would; stderr → $D/err.txt
  printf '%s' "$HOOK_STDIN" | \
    CLAUDE_PLUGIN_DATA="$PDATA" CLAUDE_CODE_SESSION_ID="$SESS" SEXTANT_STORE="$STORE" \
    ANTHROPIC_API_KEY="dummy-key" SEXTANT_STATUS_API_BASE="$APIBASE" \
    "$BIN/sextant-mcp" status 2>"$D/err.txt"
}
get_status() { "$BIN/sextant" artifact get "$ART" --json --creds "$WORKER_CREDS" --store "$STORE" 2>/dev/null; }

say "asserting the status-hook contract:"

# 1. GATING: no identity file yet ⇒ not connected ⇒ skip (no Haiku, no artifact).
run_hook
{ grep -q "not connected" "$D/err.txt" && ! get_status | grep -q headline; }
check "gating: a non-connected session skips (no Haiku, no status artifact)" $?

# Connect: seed the per-session identity file the MCP server writes on connect.
cat > "$PDATA/attest-identity/$SESS.json" <<JSON
{"creds":"$WORKER_CREDS","url":"","id":"$WORKER_ID"}
JSON

# 2. FIRE: connected ⇒ detached worker → (mock) Haiku → upsert status.<self> on the bus.
run_hook
for _ in $(seq 1 80); do get_status | grep -q "implementing the status hook prototype" && break; sleep 0.25; done
out="$(get_status)"
printf '%s' "$out" | grep -q "implementing the status hook prototype" && printf '%s' "$out" | grep -qE '"state": *"working"'
check "fire: worker upserts status.<self> {state:working, headline} on the bus" $?

# 3. THROTTLE: an immediate second fire is throttled (the 1st advanced the state).
run_hook
grep -q "throttled" "$D/err.txt"
check "throttle: an immediate second fire skips the Haiku call" $?

echo
if [ "$fail" -ne 0 ]; then
  say "RESULT: FAIL ($pass passed, $fail failed)"
  say "  last hook stderr:"; sed 's/^/    /' <"$D/err.txt"
  say "  artifact:"; get_status | sed 's/^/    /'
  exit 1
fi
say "RESULT: PASS — status hook gates, fires, and writes a live agent.status artifact ($pass checks)."