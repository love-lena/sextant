#!/usr/bin/env bash
# One-command, self-validating demo of `sextant dash --serve` (TASK-68, ADR-0032):
# the local HTTP API + zero-design web debug surface on 127.0.0.1, with the Go
# process the single bus client.
#
# It stages a throwaway bus, starts `dash --serve`, and ASSERTS the D1 contract
# end to end — the token gate, JSON parity with the CLI read commands, the publish
# command, and the SSE live stream — printing PASS/FAIL per check. Those asserts
# ARE the D1 acceptance test (it exits non-zero on any failure, so CI/a reviewer
# can run it hands-off).
#
# When run in a terminal it then keeps serving and prints the browser URL, so you
# can open the zero-design debug surface yourself: you should see the clients and
# artifacts lists populate, the live message feed update as you publish from the
# box, and `/api/self` naming this dash as the principal. Ctrl-C to stop.
set -uo pipefail

REPO="$(cd "$(dirname "$0")/../.." && pwd)"
D="$(mktemp -d /tmp/dash-serve-demo.XXXXXX)"
say()  { printf '\033[1;36m[demo]\033[0m %s\n' "$*"; }
pass=0 fail=0
check() { # check "<label>" <condition-exit-code>
  if [ "$2" -eq 0 ]; then printf '  \033[1;32m[PASS]\033[0m %s\n' "$1"; pass=$((pass+1));
  else printf '  \033[1;31m[FAIL]\033[0m %s\n' "$1"; fail=$((fail+1)); fi
}

BUS_PID="" DASH_PID="" SSE_PID=""
trap 'kill "$BUS_PID" "$DASH_PID" "$SSE_PID" 2>/dev/null || true; rm -rf "$D"' EXIT

export SEXTANT_HOME="$D/home"; STORE="$D/store"; mkdir -p "$SEXTANT_HOME" "$STORE"
BIN="$D/bin/sextant"
say "building sextant from $REPO"
mkdir -p "$D/bin"
(cd "$REPO" && go build -o "$BIN" ./clients/go/apps/sextant)

say "starting a throwaway bus"
"$BIN" up --store "$STORE" --port 0 >"$D/bus.log" 2>&1 & BUS_PID=$!
for _ in $(seq 1 80); do [ -f "$STORE/bus.json" ] && break; sleep 0.1; done
[ -f "$STORE/bus.json" ] || { say "bus did not come up"; cat "$D/bus.log"; exit 1; }

# Seed the directory + an artifact so the surface has something to show. These
# resolve the dash's own (active) context once it self-enrolls below, but the
# peer is registered up front via the operator credential under the store.
"$BIN" clients register peer --kind worker --store "$STORE" >/dev/null 2>&1

say "starting dash --serve (zero-config first run: self-enrolls, claims the principal)"
"$BIN" dash --serve --store "$STORE" --port 0 >"$D/dash.log" 2>&1 & DASH_PID=$!
URL=""
for _ in $(seq 1 80); do
  URL=$(grep -oE 'http://127\.0\.0\.1:[0-9]+/\?token=[a-f0-9]+' "$D/dash.log" | head -1 || true)
  [ -n "$URL" ] && break; sleep 0.1
done
[ -n "$URL" ] || { say "dash --serve never printed its URL"; cat "$D/dash.log"; exit 1; }
BASE=$(echo "$URL" | sed -E 's#/\?token=.*##')
TOKEN=$(echo "$URL" | sed -E 's#.*token=##')

# Now that the dash is enrolled (active context), seed an artifact through it.
"$BIN" artifact create the-plan '{"$type":"document","title":"The plan","body":"# hello from the demo"}' --store "$STORE" >/dev/null 2>&1

say "asserting the D1 contract:"

# 1. token gate — no token is rejected.
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/self")
[ "$code" = "401" ]; check "GET /api/self without a token is 401 (got $code)" $?

# 2. /api/self names this dash as the principal (claimed on first run).
self=$(curl -s -H "Authorization: Bearer $TOKEN" "$BASE/api/self")
sid=$(echo "$self" | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
sprin=$(echo "$self" | python3 -c 'import sys,json;print(json.load(sys.stdin)["principal"])')
[ -n "$sid" ] && [ "$sid" = "$sprin" ]; check "/api/self: dash id == principal (claimed first run)" $?

# 3. clients parity: the API directory matches `clients list --json`.
api_names=$(curl -s -H "Authorization: Bearer $TOKEN" "$BASE/api/clients" | python3 -c 'import sys,json;print(",".join(sorted(c["DisplayName"] for c in json.load(sys.stdin))))')
cli_names=$("$BIN" clients list --store "$STORE" --json 2>/dev/null | python3 -c 'import sys,json;print(",".join(sorted(c["DisplayName"] for c in json.load(sys.stdin))))')
[ -n "$api_names" ] && [ "$api_names" = "$cli_names" ]; check "GET /api/clients == clients list --json ($api_names)" $?

# 4. artifacts parity.
api_arts=$(curl -s -H "Authorization: Bearer $TOKEN" "$BASE/api/artifacts" | python3 -c 'import sys,json;print(",".join(sorted(a["Name"] for a in json.load(sys.stdin))))')
[ "$api_arts" = "the-plan" ]; check "GET /api/artifacts lists the-plan (got '$api_arts')" $?

# 5. publish (command) then read parity: API publish, CLI read sees it.
curl -s -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"subject":"msg.topic.demo","record":{"$type":"chat.message","text":"hello-parity"}}' "$BASE/api/publish" >/dev/null
"$BIN" read msg.topic.demo --store "$STORE" --json 2>/dev/null | grep -q "hello-parity"
check "POST /api/publish then CLI read sees the message" $?

# 6. SSE live stream: subscribe, publish, the frame arrives live.
curl -s -N "$BASE/api/stream?subject=msg.topic.live&token=$TOKEN" >"$D/sse.out" 2>/dev/null & SSE_PID=$!
sleep 0.6
curl -s -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"subject":"msg.topic.live","record":{"$type":"chat.message","text":"live-frame"}}' "$BASE/api/publish" >/dev/null
for _ in $(seq 1 20); do grep -q "live-frame" "$D/sse.out" && break; sleep 0.1; done
grep -q "live-frame" "$D/sse.out"; check "SSE stream delivered the published frame live" $?
kill "$SSE_PID" 2>/dev/null || true; SSE_PID=""

echo
if [ "$fail" -ne 0 ]; then say "RESULT: FAIL ($pass passed, $fail failed)"; exit 1; fi
say "RESULT: PASS — D1 contract verified ($pass checks)"

# Interactive vantage: hold the server up so a human can open the surface.
if [ -t 0 ]; then
  echo
  say "browser vantage — open this in your browser:"
  say "  $URL"
  say "you should see: the clients list (the dash + peer), artifacts (the-plan),"
  say "and the live message feed. Type a subject + text in the publish box and"
  say "watch it appear in the feed. Ctrl-C to stop."
  wait "$DASH_PID"
else
  say "(non-interactive: skipping the browser hand-off)"
fi
