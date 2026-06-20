#!/usr/bin/env bash
# Default capable agent recipe + Haiku auto-naming — self-validating demo.
#
# Proves the new slice on the REAL spawn path (clients/go/apps/dispatch + the default
# clients/go/apps/dispatch/recipes/agent.sh harness), deterministically and token-free:
#
#   1. NAMING: a spawn.request with NO nickname makes the dispatcher call Haiku to
#      pick a unique, evocative name and mint the child under it. We point the
#      dispatcher at a MOCK Anthropic endpoint (--api-base-url) so the naming logic
#      runs for real without tokens, and assert the minted child carries the picked
#      name — and that a collision with an existing client forces a re-pick.
#   2. CREDS ISOLATION (TASK-158): the child connects under its OWN minted ULID and
#      its OWN creds file — never the operator's or the dispatcher's. We stub
#      `claude` with a script that records the SEXTANT_CREDS the recipe handed the
#      MCP config and publishes as the child, and assert that creds file is the
#      child's, that the bus author is the child's ULID, and that it is NOT the
#      operator's id.
#   3. DM-ABLE + SELF-DIRECTION SHAPE: the stub child reads an artifact and DMs the
#      operator (the same tool calls the real recipe's role prompt drives), proving
#      the wiring an autonomous agent uses is in place.
#
# The REAL recipe (recipes/agent.sh) runs unmodified — only `claude` and the
# Anthropic endpoint are stubbed, so this exercises the production harness + the
# production naming code, not a fake of either. A live claude round-trip is the
# separate manual confirmation noted in the PR.
#
#   docs/demos/dispatch-agent-recipe-demo.sh
#
# Needs python3 (the mock Anthropic endpoint) and an environment that permits
# loopback TCP from the spawned Go dispatcher to the mock (a strict sandbox that
# drops outbound localhost makes naming fall back to agent-<id> — correct, but it
# then can't assert the Haiku pick).
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
P="${P:-/tmp/dispatch-agent-recipe}"; S="$P/store"; PORT="${PORT:-4498}"
SX="${SX:-$P/sextant}"; SXMCP="${SXMCP:-$P/sextant-mcp}"; SXDISP="${SXDISP:-$P/sextant-dispatch}"
PASS=0; FAIL=0
ok(){ echo "  PASS: $1"; PASS=$((PASS+1)); }
no(){ echo "  FAIL: $1"; FAIL=$((FAIL+1)); }

rm -rf "$P"; mkdir -p "$S" "$P/bin"
echo "== build binaries =="
( cd "$ROOT" && go build -o "$SX" ./clients/go/apps/sextant && go build -o "$SXMCP" ./clients/go/apps/mcp \
  && go build -o "$SXDISP" ./clients/go/apps/dispatch ) || { echo "build failed"; exit 2; }

# ---- Mock Anthropic endpoint for the dispatcher's Haiku naming call -----------
# Returns "kestrel" first, then "atlas" — so we can prove a collision re-pick:
# we pre-register a client named "kestrel", forcing the dispatcher to ask again.
cat >"$P/mockhaiku.py" <<'PY'
import http.server, json
PICKS = ["kestrel", "atlas", "juno"]
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get('content-length', 0)); self.rfile.read(n)
        name = PICKS.pop(0) if PICKS else "fallbackname"
        body = json.dumps({"content":[{"type":"text","text":name}]}).encode()
        self.send_response(200); self.send_header('content-type','application/json')
        self.send_header('content-length', str(len(body))); self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", 4499), H).serve_forever()
PY
python3 "$P/mockhaiku.py" & MOCK=$!
trap 'kill $DISP $MOCK 2>/dev/null; kill $BUS 2>/dev/null' EXIT; DISP=""; BUS=""
sleep 0.5

# ---- Stub `claude` -----------------------------------------------------------
# The REAL recipe builds an MCP config and runs `claude -p ... --mcp-config FILE`.
# This stub stands in for claude: it parses the same --mcp-config the recipe wrote,
# reads SEXTANT_CREDS out of it (proving the recipe pinned the CHILD's creds into
# the MCP env block), then uses the sextant CLI with THOSE creds to (a) read an
# artifact and (b) DM the operator — exactly the autonomous moves the role prompt
# drives. It records the creds path it was handed for the assertions.
cat >"$P/bin/claude" <<EOF
#!/usr/bin/env bash
# crude flag scan for --mcp-config <file>
MCPCFG=""
while [ \$# -gt 0 ]; do case "\$1" in --mcp-config) MCPCFG="\$2"; shift 2;; *) shift;; esac; done
CREDS=\$(sed -n 's/.*"SEXTANT_CREDS":"\([^"]*\)".*/\1/p' "\$MCPCFG")
ST=\$(sed -n 's/.*"SEXTANT_STORE":"\([^"]*\)".*/\1/p' "\$MCPCFG")
echo "\$CREDS" > "$P/child-creds-path.txt"
# Self-direction shape: read context, then DM the operator — as the CHILD.
"$SX" artifact get goal.demo --store "\$ST" --creds "\$CREDS" >/dev/null 2>&1
"$SX" publish msg.topic.demo '{"\$type":"chat.message","text":"on it — read goal.demo"}' --store "\$ST" --creds "\$CREDS" >/dev/null 2>&1
"$SX" publish "msg.client.\$SX_OPERATOR_ID" '{"\$type":"chat.message","text":"one question about goal.demo?"}' --store "\$ST" --creds "\$CREDS" >/dev/null 2>&1
echo '{"ok":true}'
EOF
chmod +x "$P/bin/claude"
export PATH="$P/bin:$PATH"
export SEXTANT_MCP_BIN="$SXMCP"   # the recipe requires this
export SX_AGENT_MODEL="stub"      # the stub ignores it

echo "== throwaway bus on :$PORT =="
"$SX" up --store "$S" --port "$PORT" >"$P/up.log" 2>&1 & BUS=$!
for _ in $(seq 1 100); do [ -f "$S/bus.json" ] && break; sleep 0.1; done
[ -f "$S/bus.json" ] || { echo "bus didn't start"; exit 2; }
"$SX" clients register dispatcher --kind dispatcher --store "$S" --out "$P/disp.creds" >/dev/null 2>&1
"$SX" clients register boss --kind human --store "$S" --out "$P/boss.creds" >/dev/null 2>&1
"$SX" clients register kestrel --kind agent --store "$S" --out "$P/kestrel.creds" >/dev/null 2>&1  # forces a naming collision
BOSS=$("$SX" clients list --store "$S" --creds "$P/disp.creds" | awk '/ boss /{print $1}')
export SX_OPERATOR_ID="$BOSS"
# Seed a tiny artifact the child will "gather" (self-direction context).
"$SX" artifact create goal.demo '{"$type":"document","title":"goal.demo","body":"ship v0.7.0"}' --store "$S" --creds "$P/boss.creds" >/dev/null 2>&1

reads(){ "$SX" read "$1" --since 0 --store "$S" --creds "$P/disp.creds" 2>/dev/null; }
lists(){ "$SX" clients list --store "$S" --creds "$P/disp.creds" 2>/dev/null; }
waitfor(){ local pat="$1" cmd="$2" to="${3:-20}"; for _ in $(seq 1 $((to*3))); do eval "$cmd" | grep -q "$pat" && return 0; sleep 0.34; done; return 1; }

echo "== start the dispatcher with the DEFAULT recipe + Haiku naming (mock endpoint) =="
# --on-behalf: dispatcher mints with its OWN authority (no operator creds).
# --harness recipes/agent.sh: the REAL default recipe, unmodified.
# --api-base-url: point the Haiku naming call at the mock (token-free).
"$SXDISP" --creds "$P/disp.creds" --on-behalf --store "$S" --subject msg.topic.spawn \
  --harness "$ROOT/clients/go/apps/dispatch/recipes/agent.sh" \
  --api-key "k-mock" --api-base-url "http://127.0.0.1:4499" \
  --deadline 60s >"$P/disp.log" 2>&1 & DISP=$!
sleep 1

echo "== boss publishes a spawn.request with NO nickname (and a vague brief) =="
"$SX" publish msg.topic.spawn '{"$type":"spawn.request","prompt":"summarize goal.demo and DM me one question"}' \
  --store "$S" --creds "$P/boss.creds" >/dev/null 2>&1

echo "== AC: Haiku auto-naming + collision re-pick =="
# The mock returns "kestrel" first; kestrel already exists, so the dispatcher must
# re-pick and land "atlas". The minted child must be a NEW kind=agent named atlas.
if waitfor "atlas" "lists" 25; then
  ok "dispatcher auto-named the child via Haiku and re-picked on the 'kestrel' collision → 'atlas'"
  ATLAS=$(lists | awk '/ atlas /{print $1}')
  lists | grep -qE "[[:space:]]atlas[[:space:]]+agent[[:space:]]" \
    && ok "child minted as a NAMED kind=agent ('atlas'), not agent-<id>" \
    || no "atlas not a named kind=agent in the directory"
  grep -q 'already taken; retrying' "$P/disp.log" \
    && ok "dispatcher log shows the collision re-pick path ran" \
    || no "no collision re-pick in the dispatcher log"
else
  no "child 'atlas' never appeared in the directory"; ATLAS=""; cat "$P/disp.log"
fi

echo "== AC (TASK-158): creds isolation — child acts under its OWN creds, not the operator's =="
if [ -n "${ATLAS:-}" ] && waitfor "<$ATLAS>" "reads msg.topic.demo" 25; then
  ok "child published under its OWN minted ULID ($ATLAS) — its own bus identity"
  CHILDCREDS=$(cat "$P/child-creds-path.txt" 2>/dev/null)
  case "$CHILDCREDS" in
    *"$ATLAS"*) ok "the recipe pinned the MCP at the CHILD's creds file ($CHILDCREDS contains the child id)";;
    *) no "recipe did not pin the child's creds into the MCP config (got: $CHILDCREDS)";;
  esac
  [ "$CHILDCREDS" != "$P/boss.creds" ] && [ "$CHILDCREDS" != "$P/disp.creds" ] \
    && ok "child creds are NEITHER the operator's nor the dispatcher's (TASK-158)" \
    || no "child was handed an operator/dispatcher creds file"
  reads msg.topic.demo | grep -q "<$BOSS>" && no "operator authored a child message (identity leak!)" \
    || ok "no message on the demo topic is authored by the operator id (no impersonation)"
else
  no "child never published under its own id"
fi

echo "== AC: DM-able + self-direction shape (read context, DM the operator) =="
if [ -n "${ATLAS:-}" ]; then
  waitfor "one question about goal.demo" "reads msg.client.$BOSS" 20 \
    && ok "child DMed the operator a question (self-direction: gather → ask) under id $ATLAS" \
    || no "no operator DM from the child"
  reads "msg.client.$BOSS" | grep -q "<$ATLAS>" \
    && ok "the operator DM is authored by the child's id (DM-able + own identity)" \
    || no "operator DM not authored by the child id"
fi

echo
echo "== result: $PASS passed, $FAIL failed =="
[ "$FAIL" -eq 0 ]
