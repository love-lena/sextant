#!/usr/bin/env bash
# M5.2 reference dispatcher — self-validating demo (TASK-25).
#
# Proves the dispatcher graduates the M5.1 spike (clients/go/apps/spawn-poc) into stand-up-
# on-demand: it subscribes to spawn.request, mints a NAMED child identity, launches
# the child onto the bus, supervises it (the wake loop), and acks — and a spawned
# child can itself drive the dispatcher (recursion).
#
# Runs entirely on a THROWAWAY bus (fresh store + port) and is TOKEN-FREE: the
# spawned client is a stub harness (the `sextant` CLI), not a model. M5.1 already
# proved the live `claude -p` / `codex exec` harness + resume-wake; this demo
# proves the DISPATCHER mechanism that is new in M5.2, deterministically.
#
#   docs/demos/m5-dispatcher-demo.sh
#   SX=/path/to/sextant docs/demos/m5-dispatcher-demo.sh   # reuse prebuilt binaries
#
# Design notes: docs/demos/m5-dispatcher-notes.md
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
P="${P:-/tmp/m5-dispatcher}"; S="$P/store"; PORT="${PORT:-4490}"
SX="${SX:-$P/sextant}"; SXPOC="${SXPOC:-$P/spawn-poc}"; SXDISP="${SXDISP:-$P/sextant-dispatch}"
PASS=0; FAIL=0
ok(){ echo "  PASS: $1"; PASS=$((PASS+1)); }
no(){ echo "  FAIL: $1"; FAIL=$((FAIL+1)); }

rm -rf "$P"; mkdir -p "$S"
echo "== build binaries =="
( cd "$ROOT" && go build -o "$SX" ./clients/sextant-cli && go build -o "$SXPOC" ./clients/go/apps/spawn-poc && go build -o "$SXDISP" ./clients/dispatcher ) || { echo "build failed"; exit 2; }

echo "== AC#1: spawn lexicon + records (go test) =="
( cd "$ROOT" && go test ./clients/dispatcher/ >/dev/null 2>&1 ) \
  && ok "spawn.request/spawn.ack records + lexicon files parse + round-trip (AC#1)" \
  || no "clients/dispatcher unit tests failed"

echo "== throwaway bus on :$PORT =="
"$SX" up --store "$S" --port "$PORT" >"$P/up.log" 2>&1 & BUS=$!
trap 'kill $DISP 2>/dev/null; kill $BUS 2>/dev/null' EXIT
for _ in $(seq 1 100); do [ -f "$S/bus.json" ] && break; sleep 0.1; done
[ -f "$S/bus.json" ] || { echo "bus didn't start"; exit 2; }
"$SX" clients register dispatcher --kind agent --store "$S" --out "$P/disp.creds" >/dev/null 2>&1
"$SX" clients register boss --kind human --store "$S" --out "$P/boss.creds" >/dev/null 2>&1  # the spawn.request + DM sender
reads(){ "$SX" read "$1" --since 0 --store "$S" --creds "$P/disp.creds" 2>/dev/null; }
lists(){ "$SX" clients list --store "$S" --creds "$P/disp.creds" 2>/dev/null; }
pub_as(){ "$SX" publish "$2" "$3" --store "$S" --creds "$1" >/dev/null 2>&1; }
waitfor(){ local pat="$1" subj="$2" to="${3:-20}"; for _ in $(seq 1 $((to*3))); do reads "$subj" | grep -q "$pat" && return 0; sleep 0.34; done; return 1; }

# The spawned-client harness: a one-shot stub (runs, publishes, exits — like
# `claude -p`). The dispatcher sets SEXTANT_CREDS (the child's own identity),
# SEXTANT_STORE, and the SX_* vars; $SEXTANT_BIN is inherited from this demo.
cat >"$P/child.sh" <<'EOF'
#!/usr/bin/env sh
pub(){ "$SEXTANT_BIN" publish "$1" "$2" --creds "$SEXTANT_CREDS" --store "$SEXTANT_STORE" >/dev/null 2>&1; }
if [ -n "${SX_WAKE_TEXT:-}" ]; then
  pub msg.topic.demo "{\"\$type\":\"chat.message\",\"text\":\"awake-ack from $SX_CHILD_NICK\"}"
  exit 0
fi
pub msg.topic.demo "{\"\$type\":\"chat.message\",\"text\":\"hello from $SX_CHILD_NICK\"}"
case "${SX_PROMPT:-}" in
  recurse:*)
    target="${SX_PROMPT#recurse:}"
    pub msg.topic.spawn "{\"\$type\":\"spawn.request\",\"nickname\":\"$target\",\"prompt\":\"say hello\",\"job\":\"$SX_JOB\"}"
    ;;
esac
EOF
chmod +x "$P/child.sh"
export SEXTANT_BIN="$SX"  # inherited by the dispatcher and on into each child

echo "== start the dispatcher (watches msg.topic.spawn; mints via --on-behalf, its OWN authority) =="
# `dispatcher` is a plain registered client (no operator creds, no blessed kind) —
# with --on-behalf it mints children with its own authority (mint-on-behalf,
# ADR-0033). Every child it mints is stamped a spawned worker and so cannot itself
# dispatch; recursion (AC#4) flows through the dispatcher, never the worker.
"$SXDISP" --creds "$P/disp.creds" --on-behalf \
  --store "$S" --subject msg.topic.spawn \
  --harness "$P/child.sh" --on-wake "$P/child.sh" --supervisor "$SXPOC" \
  --deadline 60s >"$P/disp.log" 2>&1 & DISP=$!
sleep 1  # let it subscribe (DeliverAll also closes the start race)

echo "== AC#2 + AC#3: boss requests a child 'alpha' (which is told to recurse into 'beta') =="
pub_as "$P/boss.creds" msg.topic.spawn '{"$type":"spawn.request","nickname":"alpha","prompt":"recurse:beta","job":"demo-job"}'
if waitfor "hello from alpha" msg.topic.demo 25; then
  ALPHA=$(lists | awk '/ alpha /{print $1}')
  ok "spawned child joined the bus and participated (AC#3 participates)"
  reads msg.topic.demo | grep -q "<$ALPHA>.*hello from alpha" \
    && ok "child published UNDER ITS OWN minted id $ALPHA (AC#3 own identity)" \
    || no "alpha hello not authored by its minted id"
  lists | grep -qE "[[:space:]]alpha[[:space:]]+agent[[:space:]]" \
    && ok "child registered as a NAMED kind=agent identity, not claude-<hex> (AC#3)" \
    || no "alpha not a named kind=agent in the directory"
  if reads msg.topic.spawn | grep '"nickname":"alpha"' | grep -q "\"id\":\"$ALPHA\".*\"status\":\"ok\""; then
    ok "dispatcher published spawn.ack with the new id + status ok (AC#2)"
  else
    no "no ok spawn.ack for alpha with id $ALPHA"
  fi
else
  no "AC#2/#3: alpha never joined"; ALPHA=""; cat "$P/disp.log"
fi

echo "== AC#4: recursion — alpha's spawn.request stood up grandchild 'beta' =="
if [ -n "${ALPHA:-}" ] && waitfor "hello from beta" msg.topic.demo 30; then
  ok "a spawned child drove the dispatcher to spawn a grandchild (AC#4 recursion)"
  reads msg.topic.spawn | grep '"nickname":"beta"' | grep -q "\"parent\":\"$ALPHA\"" \
    && ok "grandchild's lineage parent is alpha, the requesting child (AC#4 lineage)" \
    || no "beta's spawn.ack parent is not alpha"
else
  no "AC#4: beta (grandchild) never joined"
fi

echo "== AC#5: supervisor wakes alpha on a follow-up DM =="
if [ -n "${ALPHA:-}" ]; then
  sleep 1  # let alpha's supervisor (its own spawn-poc client) finish subscribing
  pub_as "$P/boss.creds" "msg.client.$ALPHA" '{"$type":"chat.message","text":"please ack you are awake"}'
  waitfor "<$ALPHA>.*awake-ack" msg.topic.demo 30 \
    && ok "supervisor re-invoked the one-shot child on a DM; it acted under id $ALPHA (AC#5 wake loop)" \
    || { no "AC#5: no awake-ack under $ALPHA"; tail -5 "$P/disp.log"; }
else
  no "AC#5 skipped: alpha never came up"
fi

echo "== AC#6: mint-on-behalf — children minted with the dispatcher's OWN authority (no operator creds) =="
# The whole run above used --on-behalf with NO --issuer-creds: alpha and beta were
# minted by the dispatcher's own bus authority (ADR-0033). The inverted fence — a
# spawned worker CANNOT itself dispatch, and a top-level client can — is proven
# rigorously in pkg/bus/mint_on_behalf_test.go; here we confirm the end-to-end path.
grep -q "mint-on-behalf (own authority" "$P/disp.log" \
  && ok "dispatcher minted via its own authority, no operator credential (AC#6 mint-on-behalf)" \
  || no "dispatcher did not use the mint-on-behalf path"
if [ -n "${ALPHA:-}" ]; then
  reads msg.topic.demo | grep -q "<$ALPHA>.*hello from alpha" \
    && ok "the mint-on-behalf child is a real bus citizen under its own id (AC#6)" \
    || no "AC#6: alpha (mint-on-behalf child) not present under its own id"
else
  no "AC#6 skipped: no mint-on-behalf child came up"
fi

echo "== result: $PASS passed, $FAIL failed =="
[ "$FAIL" -eq 0 ]
