#!/usr/bin/env bash
# One-command, self-validating demo of the per-agent status hook (TASK-87): the
# plugin's PostToolUse hook that keeps an agent's status current by calling Haiku
# on a throttle. Hermetic — no real Anthropic call: a local mock stands in for the
# API (via SEXTANT_STATUS_API_BASE), so the asserts ARE the acceptance test.
#
#   clients/claude-code/demo-status-hook.sh
#
# It drives the GENUINE `sextant-mcp status` binary exactly as the hook would
# (PostToolUse JSON on stdin + the plugin env) and proves:
#   1. GATING   — no per-session identity (a regular non-bus session) ⇒ skip,
#                 no Haiku, no failure.
#   2. FIRE     — connected ⇒ the hook fires, the detached worker reads the
#                 transcript, calls (mock) Haiku, and writes a live status.
#   3. THROTTLE — an immediate second fire is throttled (no second Haiku call).
set -uo pipefail

REPO="$(cd "$(dirname "$0")/../.." && pwd)"
D="$(mktemp -d /tmp/status-hook-demo.XXXXXX)"
BIN="$D/bin"; PDATA="$D/plugin-data"; SESS="status-demo-session"
say()  { printf '\033[1;36m[demo]\033[0m %s\n' "$*"; }
pass=0 fail=0
check() { if [ "$2" -eq 0 ]; then printf '  \033[1;32m[PASS]\033[0m %s\n' "$1"; pass=$((pass+1)); else printf '  \033[1;31m[FAIL]\033[0m %s\n' "$1"; fail=$((fail+1)); fi; }

MOCK_PID=""
trap 'kill "$MOCK_PID" 2>/dev/null || true; rm -rf "$D"' EXIT

mkdir -p "$BIN" "$PDATA/attest-identity"
say "building sextant-mcp from $REPO"
(cd "$REPO" && go build -o "$BIN/sextant-mcp" ./cmd/sextant-mcp)

# A mock Anthropic Messages API: any POST returns a canned one-line status.
say "starting a mock Anthropic endpoint (no real API call)"
cat > "$D/mock.py" <<'PY'
import http.server, json, socketserver
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        self.rfile.read(int(self.headers.get('content-length', 0)))
        body = json.dumps({"content": [{"type": "text", "text": "implementing the status hook prototype"}]}).encode()
        self.send_response(200); self.send_header('content-type', 'application/json')
        self.send_header('content-length', str(len(body))); self.end_headers(); self.wfile.write(body)
    def log_message(self, *a): pass
srv = socketserver.TCPServer(("127.0.0.1", 0), H)
print(srv.server_address[1], flush=True)
srv.serve_forever()
PY
python3 "$D/mock.py" > "$D/port.txt" 2>/dev/null & MOCK_PID=$!
for _ in $(seq 1 50); do [ -s "$D/port.txt" ] && break; sleep 0.1; done
PORT="$(cat "$D/port.txt" 2>/dev/null)"
[ -n "$PORT" ] || { say "mock did not start"; exit 1; }
APIBASE="http://127.0.0.1:$PORT"

# A synthetic transcript: recent activity the worker will digest for Haiku.
cat > "$D/transcript.jsonl" <<'JSON'
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"build the status hook prototype"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"writing the throttle + worker"},{"type":"tool_use","name":"Write","input":{}}]}}
JSON

# The PostToolUse hook stdin (what Claude Code sends the hook).
HOOK_STDIN="{\"session_id\":\"$SESS\",\"transcript_path\":\"$D/transcript.jsonl\",\"cwd\":\"$D\",\"hook_event_name\":\"PostToolUse\"}"

run_hook() { # run_hook -> stderr captured to $D/err.txt
  printf '%s' "$HOOK_STDIN" | \
    CLAUDE_PLUGIN_DATA="$PDATA" CLAUDE_CODE_SESSION_ID="$SESS" \
    ANTHROPIC_API_KEY="dummy-key" SEXTANT_STATUS_API_BASE="$APIBASE" \
    "$BIN/sextant-mcp" status 2>"$D/err.txt"
}
STATUS_FILE="$PDATA/status-$SESS.txt"

say "asserting the status-hook contract:"

# 1. GATING: no identity file yet ⇒ not connected ⇒ skip (no status, no failure).
run_hook
{ grep -q "not connected" "$D/err.txt" && [ ! -f "$STATUS_FILE" ]; }
check "gating: a non-connected session skips (no Haiku, no status file)" $?

# Connect: seed the per-session identity file the MCP server writes on connect.
cat > "$PDATA/attest-identity/$SESS.json" <<JSON
{"creds":"$D/worker.creds","url":"","id":"01STATUSDEMOWORKER000000000"}
JSON

# 2. FIRE: connected ⇒ hook fires ⇒ detached worker → (mock) Haiku → status file.
run_hook
for _ in $(seq 1 60); do [ -f "$STATUS_FILE" ] && break; sleep 0.1; done
grep -q "implementing the status hook prototype" "$STATUS_FILE" 2>/dev/null
check "fire: connected session produces a live Haiku status" $?

# 3. THROTTLE: an immediate second fire is throttled (the 1st advanced the state).
run_hook
grep -q "throttled" "$D/err.txt"
check "throttle: an immediate second fire skips the Haiku call" $?

echo
if [ "$fail" -ne 0 ]; then
  say "RESULT: FAIL ($pass passed, $fail failed)"
  say "  last hook stderr:"; sed 's/^/    /' <"$D/err.txt"
  exit 1
fi
say "RESULT: PASS — status hook gates, fires, and throttles ($pass checks)."
say "  (the status write is stubbed to a local file; the bus status primitive is the deferred sextant side — TASK-84.)"