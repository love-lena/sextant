#!/usr/bin/env bash
# One-command, self-validating demo of the direct-WS dash (TASK-180, ADR-0044):
# the browser dash is a co-equal TypeScript bus client. The Go `dash --serve` is
# reduced to a static-SPA host plus a credential-mint endpoint; the browser
# connects to the bus directly over the WebSocket listener with a short-lived,
# dash-minted, scoped credential and runs the goals/review conventions itself.
#
# It stages a THROWAWAY bus with the WebSocket listener on, starts `dash --serve`,
# and ASSERTS the Go-side contract end to end — POST /api/session mints a browser
# credential + hands back the ws URL, each tab gets a distinct credential, and the
# old /api/* relay (self/clients/messages/publish/stream/goals/artifacts) is gone
# (404) — printing PASS/FAIL per check. Those asserts ARE the AC#3 acceptance (it
# exits non-zero on any failure, so a reviewer can run it hands-off).
#
# When run in a terminal it then keeps serving and prints the browser URL, so you
# can open the designed SPA yourself: it connects over wss with a minted credential
# (watch the network panel: one POST /api/session, then a WebSocket), Home + Goals
# read live, and you can write a review verdict + sign off a goal — all over the
# bus, with no Go-backend relay. Ctrl-C to stop.
#
# The full browser-side wss path (the SPA connecting + Home/Goals/review working) is
# validated headless by the agent-browser drive at AC#5; this demo covers the
# Go-side contract a shell can assert.
set -uo pipefail

REPO="$(cd "$(dirname "$0")/../.." && pwd)"
D="$(mktemp -d /tmp/dash-direct-ws-demo.XXXXXX)"
say()  { printf '\033[1;36m[demo]\033[0m %s\n' "$*"; }
pass=0 fail=0
check() { # check "<label>" <condition-exit-code>
  if [ "$2" -eq 0 ]; then printf '  \033[1;32m[PASS]\033[0m %s\n' "$1"; pass=$((pass+1));
  else printf '  \033[1;31m[FAIL]\033[0m %s\n' "$1"; fail=$((fail+1)); fi
}

BUS_PID="" DASH_PID=""
trap 'kill "$BUS_PID" "$DASH_PID" 2>/dev/null || true; rm -rf "$D"' EXIT

# HERMETIC: a throwaway SEXTANT_HOME + store so the operator's real context is never
# touched (register/context writes land here, never ~/Library/Application Support).
export SEXTANT_HOME="$D/home"; STORE="$D/store"; mkdir -p "$SEXTANT_HOME" "$STORE"
WS_PORT=7438
BIN="$D/sextant"

say "building the dash UI + binary"
( cd "$REPO" && bash scripts/build-dash-ui.sh >/dev/null 2>&1 ) || { echo "build-dash-ui.sh failed"; exit 1; }
( cd "$REPO" && go build -o "$BIN" ./clients/sextant-cli ) || { echo "go build failed"; exit 1; }

say "enabling the bus WebSocket listener (ws://127.0.0.1:$WS_PORT) + starting the bus"
"$BIN" config set --store "$STORE" ws-listen "127.0.0.1:$WS_PORT" >/dev/null
"$BIN" up --store "$STORE" --port 0 > "$D/bus.log" 2>&1 &
BUS_PID=$!
for _ in $(seq 1 120); do [ -f "$STORE/bus.json" ] && break; sleep 0.1; done
[ -f "$STORE/bus.json" ] || { echo "bus did not come up:"; cat "$D/bus.log"; exit 1; }

say "the discovery file records the ws URL the browser dials"
grep -q "\"wsURL\": \"ws://127.0.0.1:$WS_PORT\"" "$STORE/bus.json"
check "bus.json carries wsURL ws://127.0.0.1:$WS_PORT" $?

say "starting dash --serve"
"$BIN" dash --serve --store "$STORE" --state-file "$D/dash.json" --port 0 > "$D/dash.log" 2>&1 &
DASH_PID=$!
for _ in $(seq 1 120); do [ -f "$D/dash.json" ] && break; sleep 0.1; done
[ -f "$D/dash.json" ] || { echo "dash did not come up:"; cat "$D/dash.log"; exit 1; }
BASE=$(python3 -c "import json;u=json.load(open('$D/dash.json'))['url'];print(u.split('/?')[0])")
TOKEN=$(python3 -c "import json,urllib.parse as p;u=json.load(open('$D/dash.json'))['url'];print(p.parse_qs(u.split('?',1)[1])['token'][0])")
AUTH=(-H "Authorization: Bearer $TOKEN")

say "POST /api/session mints a short-lived scoped browser credential"
SESS=$(curl -fsS -X POST "${AUTH[@]}" "$BASE/api/session")
echo "$SESS" | python3 -c "import json,sys;d=json.load(sys.stdin);assert d['id'];assert 'NATS USER JWT' in d['creds'];assert d['wsURL']=='ws://127.0.0.1:$WS_PORT'" 2>/dev/null
check "/api/session returns {id, creds(JWT), wsURL}" $?

say "each tab mints a DISTINCT credential"
SESS2=$(curl -fsS -X POST "${AUTH[@]}" "$BASE/api/session")
[ "$(echo "$SESS" | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')" != \
  "$(echo "$SESS2" | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')" ]
check "two /api/session calls mint distinct identities" $?

say "the old /api/* relay is gone (deleted — the browser calls the bus directly)"
relay_gone=0
for p in /api/self /api/clients /api/goals /api/artifacts /api/subjects "/api/messages?subject=msg.topic.x"; do
  code=$(curl -s -o /dev/null -w '%{http_code}' "${AUTH[@]}" "$BASE$p")
  [ "$code" = "404" ] || { echo "    $p returned $code, want 404"; relay_gone=1; }
done
check "every deleted /api/* relay endpoint returns 404" $relay_gone

say "the survivors still serve (static SPA host + the mint endpoint)"
survivors_ok=0
for p in / /debug /build.json; do
  code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE$p")
  [ "$code" = "200" ] || { echo "    $p returned $code, want 200"; survivors_ok=1; }
done
check "GET / , /debug , /build.json all 200" $survivors_ok

echo
say "RESULT: $pass passed, $fail failed"
[ "$fail" -eq 0 ] || exit 1

# Interactive tail: keep serving so you can open the SPA in a browser.
if [ -t 1 ]; then
  echo
  say "open the dash in a browser to see it connect over wss with a minted credential:"
  say "  $(python3 -c "import json;print(json.load(open('$D/dash.json'))['url'])")"
  say "  (Home + Goals read live; write a review verdict + sign off a goal — all over the bus.)"
  say "Ctrl-C to stop."
  wait "$DASH_PID"
fi
