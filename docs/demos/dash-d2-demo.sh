#!/usr/bin/env bash
# One-command, self-validating demo of the frontend-dash D2 UI (TASK-71): the
# intentionally-designed "cockpit" served by `sextant dash --serve` over the
# verified D1 local API (ADR-0032).
#
# It stages a throwaway bus, starts `dash --serve`, and ASSERTS the D2 contract:
#   - / serves the designed app (vendored React + precompiled JS, no runtime CDN
#     or in-browser Babel); the zero-design debug surface moved to /debug.
#   - the review convention persists a review-state + the reviewed revision onto
#     the artifact record, and rejects unknown states.
#   - the subject registry (/api/subjects) lists subjects the dash has seen, so
#     the UI can populate its conversation list on load.
# Those asserts ARE the D2 acceptance test (it exits non-zero on any failure).
# When `agent-browser` is on PATH it also drives the real UI headless and asserts
# it mounts and renders live bus data with the special-cased `home` artifact hidden.
#
# When run in a terminal it then holds the server up and prints the browser URL so
# you can open the cockpit yourself. Ctrl-C to stop.
set -uo pipefail

REPO="$(cd "$(dirname "$0")/../.." && pwd)"
D="$(mktemp -d /tmp/dash-d2-demo.XXXXXX)"
say()  { printf '\033[1;36m[demo]\033[0m %s\n' "$*"; }
pass=0 fail=0
check() { # check "<label>" <condition-exit-code>
  if [ "$2" -eq 0 ]; then printf '  \033[1;32m[PASS]\033[0m %s\n' "$1"; pass=$((pass+1));
  else printf '  \033[1;31m[FAIL]\033[0m %s\n' "$1"; fail=$((fail+1)); fi
}

BUS_PID="" DASH_PID=""
trap 'kill "$BUS_PID" "$DASH_PID" 2>/dev/null || true; command -v agent-browser >/dev/null 2>&1 && agent-browser close --all >/dev/null 2>&1; rm -rf "$D"' EXIT

export SEXTANT_HOME="$D/home"; STORE="$D/store"; mkdir -p "$SEXTANT_HOME" "$STORE"
BIN="$D/bin/sextant"
say "building sextant from $REPO (embeds the committed, precompiled UI)"
mkdir -p "$D/bin"
(cd "$REPO" && go build -o "$BIN" ./clients/go/apps/sextant)

say "starting a throwaway bus"
"$BIN" up --store "$STORE" --port 0 >"$D/bus.log" 2>&1 & BUS_PID=$!
for _ in $(seq 1 80); do [ -f "$STORE/bus.json" ] && break; sleep 0.1; done
[ -f "$STORE/bus.json" ] || { say "bus did not come up"; cat "$D/bus.log"; exit 1; }

"$BIN" clients register research-agent --kind worker --store "$STORE" >/dev/null 2>&1

say "starting dash --serve (zero-config: self-enrolls, claims the principal)"
"$BIN" dash --serve --store "$STORE" --port 0 >"$D/dash.log" 2>&1 & DASH_PID=$!
URL=""
for _ in $(seq 1 80); do
  URL=$(grep -oE 'http://127\.0\.0\.1:[0-9]+/\?token=[a-f0-9]+' "$D/dash.log" | head -1 || true)
  [ -n "$URL" ] && break; sleep 0.1
done
[ -n "$URL" ] || { say "dash --serve never printed its URL"; cat "$D/dash.log"; exit 1; }
BASE=$(echo "$URL" | sed -E 's#/\?token=.*##')
TOKEN=$(echo "$URL" | sed -E 's#.*token=##')
AUTH="Authorization: Bearer $TOKEN"

# Seed artifacts through the dash's active context: a brief to review + the
# special-cased home config artifact.
"$BIN" artifact create q3-brief '{"$type":"document","title":"Q3 Brief","body":"# Q3 Brief\n\nNeeds your review."}' --store "$STORE" >/dev/null 2>&1
"$BIN" artifact create home '{"$type":"sextant.home","greeting":{"eyebrow":"X","heading":"Home","note":"n","signedBy":"o","updated":"now"},"banner":{"caption":"c"},"blocks":[]}' --store "$STORE" >/dev/null 2>&1

say "asserting the D2 contract:"

# 1. / serves the designed app (vendored + precompiled), not the debug page.
root=$(curl -s "$BASE/")
{ echo "$root" | grep -q 'id="root"' && echo "$root" | grep -q 'app.js' && echo "$root" | grep -q 'vendor/react'; }
check "GET / serves the designed app (root + precompiled app.js + vendored React)" $?

# 2. no runtime CDN or in-browser Babel in the served page.
! echo "$root" | grep -qE 'unpkg|jsdelivr|/babel'
check "served app has no runtime CDN / Babel reference" $?

# 3. the zero-design debug surface still works, at /debug.
dbg=$(curl -s "$BASE/debug")
{ echo "$dbg" | grep -q 'EventSource' && echo "$dbg" | grep -q '/api/stream'; }
check "GET /debug serves the zero-design surface (stream + token wiring)" $?

# 4. review convention: approve persists state + the reviewed revision.
curl -s -o /dev/null -H "$AUTH" -H "Content-Type: application/json" \
  -d '{"state":"approved"}' -X POST "$BASE/api/artifacts/q3-brief/review"
rec=$(curl -s -H "$AUTH" "$BASE/api/artifacts/q3-brief")
echo "$rec" | python3 -c 'import sys,json; r=json.load(sys.stdin)["Record"].get("review",{}); sys.exit(0 if r.get("state")=="approved" and "rev" in r else 1)'
check "POST /api/artifacts/{name}/review persists review.state=approved + review.rev" $?

# 5. review rejects an unknown state.
code=$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" -H "Content-Type: application/json" \
  -d '{"state":"banana"}' -X POST "$BASE/api/artifacts/q3-brief/review")
[ "$code" = "400" ]; check "review rejects an unknown state (got $code)" $?

# 6. subject registry: a published subject shows up in /api/subjects.
curl -s -o /dev/null -H "$AUTH" -H "Content-Type: application/json" \
  -d '{"subject":"msg.topic.alpha","record":{"$type":"chat.message","text":"hi"}}' -X POST "$BASE/api/publish"
for _ in $(seq 1 20); do
  curl -s -H "$AUTH" "$BASE/api/subjects" | grep -q "msg.topic.alpha" && break; sleep 0.1
done
curl -s -H "$AUTH" "$BASE/api/subjects" | grep -q "msg.topic.alpha"
check "GET /api/subjects lists a published subject (conversation discovery)" $?

# 7. (optional) drive the real UI headless: mounts, renders live data, hides home.
if command -v agent-browser >/dev/null 2>&1; then
  agent-browser open "$URL" >/dev/null 2>&1
  agent-browser wait 5000 >/dev/null 2>&1
  # Assert what's visible by default: the app mounts, the live artifact renders in
  # the (open) Artifacts list, and the special-cased `home` artifact is NOT listed.
  ui=$(agent-browser eval 'var b=document.body.innerText; JSON.stringify({k:document.getElementById("root").children.length, art:b.indexOf("q3-brief")>=0, homeRow:[...document.querySelectorAll(".sx-aname")].some(e=>e.innerText==="home")})' 2>/dev/null | tail -1)
  echo "$ui" | python3 -c 'import sys,json
s=sys.stdin.read().strip()
try:
    d=json.loads(s)
    if isinstance(d,str): d=json.loads(d)
except Exception:
    sys.exit(1)
sys.exit(0 if d.get("k",0)>=1 and d.get("art") and not d.get("homeRow") else 1)' 2>/dev/null
  check "designed UI mounts + renders the live artifact, with home hidden" $?
  # dark mode: clicking the topbar toggle flips #app.dark. Click then wait (React
  # state/effect are async), and compare before/after so it's state-independent.
  b4=$(agent-browser eval 'document.getElementById("app").classList.contains("dark")?1:0' 2>/dev/null | tail -1 | tr -dc '01')
  agent-browser eval '(function(){var b=[...document.querySelectorAll(".sx-icon-btn")].find(x=>/mode/i.test(x.title||""));if(b)b.click();})()' >/dev/null 2>&1
  agent-browser wait 800 >/dev/null 2>&1
  af=$(agent-browser eval 'document.getElementById("app").classList.contains("dark")?1:0' 2>/dev/null | tail -1 | tr -dc '01')
  [ -n "$b4" ] && [ -n "$af" ] && [ "$b4" != "$af" ]
  check "dark-mode toggle flips the cockpit theme" $?
  agent-browser close --all >/dev/null 2>&1 || true
else
  say "(agent-browser not on PATH — skipping the headless UI render check)"
fi

echo
if [ "$fail" -ne 0 ]; then say "RESULT: FAIL ($pass passed, $fail failed)"; exit 1; fi
say "RESULT: PASS — D2 contract verified ($pass checks)"

if [ -t 0 ]; then
  echo
  say "browser vantage — open the cockpit:"
  say "  $URL"
  say "you should see: the curated Home, the sidebar (Conversations / Artifacts by"
  say "review-state / Goals / Agents), and the q3-brief artifact (Approve it, or open"
  say "its Discussion). Ctrl-C to stop."
  wait "$DASH_PID"
else
  say "(non-interactive: skipping the browser hand-off)"
fi
