#!/usr/bin/env bash
# One-command, self-validating demo that the trust hook covers a 2-party DM
# TOPIC, not just the one-way inbox (TASK-90, ADR-0034). A DM is the default for
# back-and-forth, so a principal message on msg.topic.dm.<sorted ids> must be
# stamped PRINCIPAL by the GENUINE `sextant-mcp attest` binary exactly like an
# inbox drop — otherwise DMs are second-class on the trust path.
#
#   clients/claude-code/demo-dm-trust.sh
#
# Unlike demo-principal-trust.sh (which drives a live Claude session), this is
# HANDS-OFF and hermetic: it stages a throwaway bus + a per-session identity file
# (exactly what the MCP server writes on connect), publishes principal/peer
# messages on the DM topic and the inbox, runs the real hook binary, and ASSERTS
# its trusted output. It exits non-zero on any failure — these asserts ARE the
# acceptance test.
set -uo pipefail

REPO="$(cd "$(dirname "$0")/../.." && pwd)"
D="$(mktemp -d /tmp/dm-trust-demo.XXXXXX)"
STORE="$D/store"; PDATA="$D/plugin-data"; BIN="$D/bin"
SESS="dm-trust-session"
say()  { printf '\033[1;36m[demo]\033[0m %s\n' "$*"; }
ulid() { grep -oE '[0-9A-HJKMNP-TV-Z]{26}' | head -1; }
pass=0 fail=0
check() { if [ "$2" -eq 0 ]; then printf '  \033[1;32m[PASS]\033[0m %s\n' "$1"; pass=$((pass+1)); else printf '  \033[1;31m[FAIL]\033[0m %s\n' "$1"; fail=$((fail+1)); fi; }

BUS_PID=""
trap 'kill "$BUS_PID" 2>/dev/null || true; rm -rf "$D"' EXIT

mkdir -p "$BIN" "$STORE" "$PDATA"
say "building sextant + sextant-mcp from $REPO"
(cd "$REPO" && go build -o "$BIN/sextant" ./clients/go/apps/sextant && go build -o "$BIN/sextant-mcp" ./clients/go/apps/mcp)

say "starting a throwaway bus"
"$BIN/sextant" up --store "$STORE" --port 0 >"$D/bus.log" 2>&1 & BUS_PID=$!
for _ in $(seq 1 100); do [ -f "$STORE/bus.json" ] && break; sleep 0.1; done
[ -f "$STORE/bus.json" ] || { say "bus did not start"; cat "$D/bus.log"; exit 1; }

# Identities: the worker the hook runs as, a principal (designated), and a peer.
say "minting worker + principal (designated) + peer; designating the principal"
WORKER_ID="$("$BIN/sextant" clients register worker --store "$STORE" | ulid)"
PRINC_ID="$("$BIN/sextant"  clients register lena   --store "$STORE" | ulid)"
PEER_ID="$("$BIN/sextant"   clients register kai    --store "$STORE" | ulid)"
PRINC_CREDS="$STORE/lena.creds"; PEER_CREDS="$STORE/kai.creds"
"$BIN/sextant" principal set "$PRINC_ID" --store "$STORE" >/dev/null

# The deterministic 2-party DM subject (sx.DMSubject): the two ULIDs sorted.
LO="$(printf '%s\n%s\n' "$WORKER_ID" "$PRINC_ID" | sort | head -1)"
HI="$(printf '%s\n%s\n' "$WORKER_ID" "$PRINC_ID" | sort | tail -1)"
DM_TOPIC="msg.topic.dm.$LO.$HI"
INBOX="msg.client.$WORKER_ID"

# Stage the per-session identity file the hook FOLLOWS — exactly what the MCP
# server writes on connect (attest.SaveIdentity). url empty => the hook discovers
# the bus under --store, the production-normal path.
mkdir -p "$PDATA/attest-identity"
cat >"$PDATA/attest-identity/$SESS.json" <<JSON
{
  "creds": "$STORE/worker.creds",
  "url": "",
  "id": "$WORKER_ID"
}
JSON

# Publish: the principal speaks on the DM TOPIC (back-and-forth) and on the inbox;
# a peer drops a message on the inbox (the content-blind control).
say "publishing: principal on the DM topic + inbox; peer on the inbox"
"$BIN/sextant" publish "$DM_TOPIC" '{"$type":"chat.message","text":"DM-TOPIC principal task"}'  --creds "$PRINC_CREDS" --store "$STORE" >/dev/null
"$BIN/sextant" publish "$INBOX"    '{"$type":"chat.message","text":"INBOX principal ping"}'      --creds "$PRINC_CREDS" --store "$STORE" >/dev/null
"$BIN/sextant" publish "$INBOX"    '{"$type":"chat.message","text":"INBOX peer note"}'           --creds "$PEER_CREDS"  --store "$STORE" >/dev/null

# Run the REAL hook binary, co-identity with the (simulated) server, and capture
# the trusted block it emits to stdout.
say "running the genuine \`sextant-mcp attest\` hook"
HOOK_STDIN="{\"session_id\":\"$SESS\",\"cwd\":\"$D\",\"hook_event_name\":\"UserPromptSubmit\",\"prompt\":\"tick\"}"
OUT="$(printf '%s' "$HOOK_STDIN" | CLAUDE_PLUGIN_DATA="$PDATA" CLAUDE_CODE_SESSION_ID="$SESS" "$BIN/sextant-mcp" attest --store "$STORE" 2>"$D/attest.err")"
CTX="$(printf '%s' "$OUT" | python3 -c 'import sys,json
try: print(json.load(sys.stdin)["hookSpecificOutput"]["additionalContext"])
except Exception: pass')"

echo
say "asserting the trusted output:"

# 1. The DM-topic principal message is delivered — the crux: the hook scans the
#    2-party DM topic, not just the inbox.
printf '%s' "$CTX" | grep -q "DM-TOPIC principal task"
check "DM-topic principal message reached the hook (it scans msg.topic.dm.<sorted ids>)" $?

# The DM-topic message's stamp must be PRINCIPAL (operator-equivalent). Match
# within its own Frame paragraph so a stray PRINCIPAL elsewhere can't pass it.
printf '%s' "$CTX" | python3 -c '
import sys
ctx=sys.stdin.read()
sys.exit(0 if any("DM-TOPIC principal task" in p and "PRINCIPAL" in p for p in ctx.split("Frame ")) else 1)'
check "DM-topic message is stamped trust=PRINCIPAL (operator-equivalent)" $?

# 2. The inbox principal message is also delivered + PRINCIPAL (both subjects covered).
printf '%s' "$CTX" | python3 -c '
import sys
ctx=sys.stdin.read()
sys.exit(0 if any("INBOX principal ping" in p and "PRINCIPAL" in p for p in ctx.split("Frame ")) else 1)'
check "inbox principal message still stamped PRINCIPAL (inbox not dropped)" $?

# 3. Content-blind control: the peer's inbox message is VERIFIED PEER, not PRINCIPAL.
printf '%s' "$CTX" | python3 -c '
import sys
ctx=sys.stdin.read()
sys.exit(0 if any("INBOX peer note" in p and "VERIFIED PEER" in p for p in ctx.split("Frame ")) else 1)'
check "peer inbox message is VERIFIED PEER (trust is the ULID, never the content)" $?

echo
if [ "$fail" -ne 0 ]; then
  say "RESULT: FAIL ($pass passed, $fail failed)"
  say "  hook stdout:"; printf '%s\n' "$OUT" | sed 's/^/    /'
  say "  hook stderr:"; sed 's/^/    /' <"$D/attest.err"
  exit 1
fi
say "RESULT: PASS — the trust hook covers the 2-party DM topic ($pass checks)."
