#!/usr/bin/env bash
# One-command, self-validating demo of the v0.4.0 dash chat-polish batch
# (TASK-102/103/104/105): artifact links, markdown rendering, a wrapping compose
# box, and operator-self messages.
#
# It stages a throwaway bus + `dash --serve`, seeds an agent message (multiline
# markdown that mentions an artifact) and a self ("you") message, then — when
# `agent-browser` is on PATH — drives the real UI headless and ASSERTS:
#   - TASK-102: a mentioned artifact name renders as a clickable .sx-artlink.
#   - TASK-103: light markdown renders (bold / inline code / bullet list) with no
#               raw markdown syntax leaking through.
#   - TASK-104: typing a long draft grows the compose box vertically (wraps) and
#               never overflows it horizontally.
#   - TASK-105: the agent message is AGENT-badged; the operator's own message is
#               self-styled, labelled "you", and carries no AGENT badge.
# Those asserts ARE the acceptance test (exits non-zero on any failure).
#
# Run in a terminal and it then holds the server up + prints the URL so you can
# open the cockpit and click the artifact link yourself. Ctrl-C to stop.
set -uo pipefail

REPO="$(cd "$(dirname "$0")/../.." && pwd)"
D="$(mktemp -d /tmp/dash-polish-demo.XXXXXX)"
say()  { printf '\033[1;36m[demo]\033[0m %s\n' "$*"; }
pass=0 fail=0
check() { if [ "$2" -eq 0 ]; then printf '  \033[1;32m[PASS]\033[0m %s\n' "$1"; pass=$((pass+1)); else printf '  \033[1;31m[FAIL]\033[0m %s\n' "$1"; fail=$((fail+1)); fi; }

BUS_PID="" DASH_PID=""
trap 'kill "$BUS_PID" "$DASH_PID" 2>/dev/null || true; command -v agent-browser >/dev/null 2>&1 && agent-browser close --all >/dev/null 2>&1; rm -rf "$D"' EXIT

export SEXTANT_HOME="$D/home"; STORE="$D/store"; mkdir -p "$SEXTANT_HOME" "$STORE"
BIN="$D/bin/sextant"; mkdir -p "$D/bin"
say "building sextant from $REPO (embeds the committed, precompiled UI)"
(cd "$REPO" && go build -o "$BIN" ./clients/sextant-cli)

say "starting a throwaway bus"
"$BIN" up --store "$STORE" --port 0 >"$D/bus.log" 2>&1 & BUS_PID=$!
for _ in $(seq 1 80); do [ -f "$STORE/bus.json" ] && break; sleep 0.1; done
[ -f "$STORE/bus.json" ] || { say "bus did not come up"; cat "$D/bus.log"; exit 1; }

say "registering an agent identity (kind=worker → renders AGENT-badged)"
REG="$("$BIN" clients register stella --kind worker --store "$STORE" 2>&1)"
ACREDS="$(printf '%s\n' "$REG" | grep -oE '/[^ ]+\.creds' | head -1)"

say "starting dash --serve (self-enrolls + claims principal → its msgs are 'you')"
"$BIN" dash --serve --store "$STORE" --port 0 >"$D/dash.log" 2>&1 & DASH_PID=$!
URL=""
for _ in $(seq 1 80); do
  URL="$(grep -oE 'http://127\.0\.0\.1:[0-9]+/\?token=[a-f0-9]+' "$D/dash.log" | head -1 || true)"
  [ -n "$URL" ] && break; sleep 0.1
done
[ -n "$URL" ] || { say "dash did not come up"; cat "$D/dash.log"; exit 1; }
BASE="$(printf '%s' "$URL" | grep -oE 'http://127\.0\.0\.1:[0-9]+')"
TOKEN="$(printf '%s' "$URL" | grep -oE 'token=[a-f0-9]+' | cut -d= -f2)"

say "seeding an artifact (a name for chat to linkify) + the home config"
"$BIN" artifact create release-brief '{"$type":"document","title":"Release Brief","body":"# Release Brief\n\nThe v0.4.0 dash-polish batch."}' --store "$STORE" >/dev/null 2>&1
"$BIN" artifact create home '{"$type":"sextant.home","greeting":{"eyebrow":"X","heading":"Home","note":"n","signedBy":"o","updated":"now"},"banner":{"caption":"c"},"blocks":[]}' --store "$STORE" >/dev/null 2>&1

say "seeding an AGENT message (multiline + markdown + an artifact mention)"
"$BIN" publish msg.topic.crew '{"$type":"chat.message","text":"Dash-polish batch is up. Four changes:\n\n- **artifact links** — names like release-brief are now clickable\n- newline + light markdown rendering (this list!)\n- compose box wraps with `inline code` support\n- your own messages read as you\n\nSee release-brief for the writeup."}' --creds "$ACREDS" --store "$STORE" >/dev/null 2>&1

say "seeding a SELF message via /api/publish (authored as the dash → 'you')"
curl -s -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"subject":"msg.topic.crew","record":{"$type":"chat.message","text":"Looks great — I will merge after sirius gates it.\n\nThanks stella!"}}' \
  -X POST "$BASE/api/publish" >/dev/null 2>&1

if command -v agent-browser >/dev/null 2>&1; then
  say "driving the real UI headless"
  agent-browser open "$URL" >/dev/null 2>&1
  agent-browser wait 6000 >/dev/null 2>&1   # let the subjects poll surface #crew
  agent-browser eval '(function(){var it=[...document.querySelectorAll(".sx-citem")].find(e=>/crew/i.test(e.innerText));if(it)it.click();})()' >/dev/null 2>&1
  agent-browser wait 2500 >/dev/null 2>&1

  # message-render assertions (return a compact JSON, parse with python)
  R="$(agent-browser eval '(function(){
    var msgs=[...document.querySelectorAll(".sx-msg")];
    var agent=msgs.find(function(m){return m.querySelector(".sx-tag-agent");});
    var self=document.querySelector(".sx-msg.is-self");
    var html=agent?agent.querySelector(".sx-msg-text").innerHTML:"";
    return JSON.stringify({
      artlink: document.querySelectorAll(".sx-msg-text a.sx-artlink[data-art]").length,
      bold: /<strong>/.test(html), code: /<code>/.test(html), list: /<li>/.test(html),
      noleak: !/[*][*]/.test(html),
      agentbadge: !!agent,
      self_you: !!(self && /^you$/i.test(self.querySelector(".sx-msg-name").innerText.trim())),
      self_nobadge: !!(self && !self.querySelector(".sx-tag-agent"))
    });
  })()' 2>/dev/null | tail -1)"
  pj() { printf '%s' "$R" | python3 -c 'import sys,json;s=sys.stdin.read().strip();d=json.loads(s);d=json.loads(d) if isinstance(d,str) else d;print(d.get(sys.argv[1]))' "$1" 2>/dev/null; }
  [ "$(pj artlink)" != "0" ] && [ -n "$(pj artlink)" ]; check "TASK-102 artifact name renders as a clickable .sx-artlink ($(pj artlink) found)" $?
  [ "$(pj bold)" = "True" ] && [ "$(pj code)" = "True" ] && [ "$(pj list)" = "True" ]; check "TASK-103 markdown renders (bold + inline code + list)" $?
  [ "$(pj noleak)" = "True" ]; check "TASK-103 no raw markdown syntax leaks through" $?
  [ "$(pj agentbadge)" = "True" ] && [ "$(pj self_you)" = "True" ] && [ "$(pj self_nobadge)" = "True" ]; check "TASK-105 agent is AGENT-badged; self is 'you' with no badge" $?

  # TASK-104: type a long draft → the compose box grows vertically, not sideways.
  agent-browser eval '(function(){var t=document.querySelector(".sx-input");var set=Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype,"value").set;set.call(t,"A long draft reply that should wrap onto a second and third line inside the compose box instead of pushing the input off to the right edge of the pane.");t.dispatchEvent(new Event("input",{bubbles:true}));})()' >/dev/null 2>&1
  agent-browser wait 800 >/dev/null 2>&1
  T="$(agent-browser eval '(function(){var t=document.querySelector(".sx-input");return JSON.stringify({h:t.clientHeight, ov:t.scrollWidth-t.clientWidth, tag:t.tagName});})()' 2>/dev/null | tail -1)"
  th="$(printf '%s' "$T" | python3 -c 'import sys,json;d=json.loads(json.loads(sys.stdin.read()));print(d["h"])' 2>/dev/null)"
  tov="$(printf '%s' "$T" | python3 -c 'import sys,json;d=json.loads(json.loads(sys.stdin.read()));print(d["ov"])' 2>/dev/null)"
  ttag="$(printf '%s' "$T" | python3 -c 'import sys,json;d=json.loads(json.loads(sys.stdin.read()));print(d["tag"])' 2>/dev/null)"
  [ "$ttag" = "TEXTAREA" ] && [ "${th:-0}" -gt 30 ] && [ "${tov:-99}" -le 2 ]; check "TASK-104 compose box grows vertically (h=${th}px) without horizontal overflow (ov=${tov}px)" $?

  agent-browser screenshot "$D/chat.png" >/dev/null 2>&1
  agent-browser close --all >/dev/null 2>&1 || true
else
  say "(agent-browser not on PATH — skipping the headless UI assertions)"
fi

echo
if [ "$fail" -ne 0 ]; then say "RESULT: FAIL ($pass passed, $fail failed)"; exit 1; fi
say "RESULT: PASS — dash chat-polish verified ($pass checks)"

if [ -t 0 ]; then
  echo
  say "open the cockpit and click the #crew conversation:"
  say "  $URL"
  say "you should see: stella AGENT-badged with a bulleted markdown message and"
  say "green 'release-brief' links; your own reply labelled 'you' in a tinted"
  say "bubble; and a compose box that wraps. Ctrl-C to stop."
  wait "$DASH_PID"
else
  say "(non-interactive: skipping the browser hand-off)"
fi
