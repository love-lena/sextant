#!/usr/bin/env bash
# M5.4 workflow coordinator — self-validating demo (TASK-26).
#
# The end-to-end M5 composition: the workflow coordinator (M5.4) drives a
# declarative workflow whose steps DISPATCH agents through the M5.2 dispatcher
# (cmd/sextant-dispatch), checkpointing state to an Artifact (CAS), emitting an
# event stream, resuming idempotently, and honouring cooperative control.
#
# Runs on a THROWAWAY bus and is TOKEN-FREE: the dispatched agent is a stub (the
# `sextant` CLI) that reports its step done — M5.1 already proved the live
# claude -p / codex exec harness. This proves the COORDINATOR + its composition.
#
#   docs/demos/m5-workflow-demo.sh
#   SX=/path/to/sextant docs/demos/m5-workflow-demo.sh   # reuse prebuilt binaries
#
# Design notes: docs/demos/m5-workflow-notes.md
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
P="${P:-/tmp/m5-workflow}"; S="$P/store"; PORT="${PORT:-4495}"
SX="${SX:-$P/sextant}"; SXPOC="${SXPOC:-$P/spawn-poc}"; SXDISP="${SXDISP:-$P/sextant-dispatch}"; SXWF="${SXWF:-$P/sextant-workflow}"
PASS=0; FAIL=0
ok(){ echo "  PASS: $1"; PASS=$((PASS+1)); }
no(){ echo "  FAIL: $1"; FAIL=$((FAIL+1)); }

rm -rf "$P"; mkdir -p "$S"
echo "== build binaries =="
( cd "$ROOT" && go build -o "$SX" ./cmd/sextant && go build -o "$SXPOC" ./cmd/spawn-poc \
  && go build -o "$SXDISP" ./cmd/sextant-dispatch && go build -o "$SXWF" ./cmd/sextant-workflow ) || { echo "build failed"; exit 2; }

echo "== AC#1/#3: workflow records + lexicons (go test) =="
( cd "$ROOT" && go test ./cmd/sextant-workflow/ >/dev/null 2>&1 ) \
  && ok "sextant.workflow/v1 + event/control records + lexicons parse + nextPending resume logic (AC#1/#3)" \
  || no "cmd/sextant-workflow unit tests failed"

echo "== throwaway bus on :$PORT =="
"$SX" up --store "$S" --port "$PORT" >"$P/up.log" 2>&1 & BUS=$!
trap 'kill $DISP $WF 2>/dev/null; kill $BUS 2>/dev/null' EXIT; DISP=""; WF=""
for _ in $(seq 1 100); do [ -f "$S/bus.json" ] && break; sleep 0.1; done
[ -f "$S/bus.json" ] || { echo "bus didn't start"; exit 2; }
"$SX" clients register dispatcher --kind agent --store "$S" --out "$P/disp.creds" >/dev/null 2>&1
"$SX" clients register coordinator --kind agent --store "$S" --out "$P/coord.creds" >/dev/null 2>&1
"$SX" clients register boss --kind human --store "$S" --out "$P/boss.creds" >/dev/null 2>&1
reads(){ "$SX" read "$1" --since 0 --store "$S" --creds "$P/coord.creds" 2>/dev/null; }
lists(){ "$SX" clients list --store "$S" --creds "$P/coord.creds" 2>/dev/null; }
# state prints the workflow envelope compacted to one line (the CLI pretty-prints
# with spaces; strip them so the grep patterns are format-tolerant).
state(){ "$SX" artifact get "workflow.$1" --json --store "$S" --creds "$P/coord.creds" 2>/dev/null | tr -d ' \n'; }
pub_as(){ "$SX" publish "$2" "$3" --store "$S" --creds "$1" >/dev/null 2>&1; }
waitfor(){ local pat="$1" cmd="$2" to="${3:-25}"; for _ in $(seq 1 $((to*3))); do eval "$cmd" | grep -q "$pat" && return 0; sleep 0.34; done; return 1; }

# Workflow-aware stub harness: announces itself and, when dispatched as a workflow
# step (WF_EVENTS + WF_STEP in the prompt), reports that step done on the workflow's
# event stream under its OWN minted identity.
cat >"$P/child.sh" <<'EOF'
#!/usr/bin/env sh
pub(){ "$SEXTANT_BIN" publish "$1" "$2" --creds "$SEXTANT_CREDS" --store "$SEXTANT_STORE" >/dev/null 2>&1; }
pub msg.topic.demo "{\"\$type\":\"chat.message\",\"text\":\"hello from $SX_CHILD_NICK\"}"
ev=$(printf '%s' "$SX_PROMPT" | sed -n 's/.*WF_EVENTS=\([^ ]*\).*/\1/p')
st=$(printf '%s' "$SX_PROMPT" | sed -n 's/.*WF_STEP=\([^ ]*\).*/\1/p')
[ -n "$ev" ] && [ -n "$st" ] && pub "$ev" "{\"\$type\":\"workflow.event\",\"step\":\"$st\",\"status\":\"done\",\"by\":\"$SX_CHILD_ID\"}"
EOF
chmod +x "$P/child.sh"
export SEXTANT_BIN="$SX"

echo "== start the M5.2 dispatcher (--on-behalf; the coordinator will compose it) =="
"$SXDISP" --creds "$P/disp.creds" --on-behalf --store "$S" --subject msg.topic.spawn \
  --harness "$P/child.sh" --deadline 120s >"$P/disp.log" 2>&1 & DISP=$!
sleep 1

echo "== AC#2 + composition: run a 2-step workflow that dispatches agents =="
cat >"$P/plan.json" <<EOF
{"id":"wfdemo","steps":[
  {"id":"review","kind":"dispatch","nickname":"reviewer","prompt":"review the change"},
  {"id":"merge","kind":"dispatch","nickname":"merger","prompt":"merge the change"}
]}
EOF
"$SXWF" --creds "$P/coord.creds" --store "$S" --plan "$P/plan.json" --id wfdemo \
  --spawn-subject msg.topic.spawn --step-timeout 60s >"$P/wf.log" 2>&1 & WF=$!
if waitfor 'workflow wfdemo: done' "cat $P/wf.log" 40; then
  ok "coordinator walked the steps to completion, checkpointing state (AC#2)"
  state wfdemo | grep -q '"\$type":"sextant.workflow/v1"' && state wfdemo | grep -q '"owner":"' \
    && ok "state envelope is a versioned sextant.workflow/v1 record with owner + steps (AC#1/#3)" \
    || no "state envelope missing \$type/owner"
  # workflow status + both step statuses checkpointed done == three "done"s.
  DONES=$(state wfdemo | grep -o '"status":"done"' | wc -l | tr -d ' ')
  state wfdemo | grep -q '"id":"review"' && state wfdemo | grep -q '"id":"merge"' && [ "$DONES" -ge 3 ] \
    && ok "both steps + the workflow checkpointed done in the state artifact (AC#2)" \
    || no "steps not both done in state (dones=$DONES)"
  lists | grep -qE "[[:space:]]reviewer[[:space:]]+agent[[:space:]]" && lists | grep -qE "[[:space:]]merger[[:space:]]+agent[[:space:]]" \
    && ok "each step DISPATCHED a named agent via the M5.2 dispatcher (end-to-end composition)" \
    || no "dispatched agents reviewer/merger not in the directory"
  reads msg.workflow.wfdemo.events | grep -q '"step":"review","status":"done"' \
    && reads msg.workflow.wfdemo.events | grep -q '"step":"merge","status":"done"' \
    && ok "the workflow event stream carries per-step transitions (AC#2 events)" \
    || no "event stream missing step-done events"
else
  no "AC#2: workflow never reached done"; cat "$P/wf.log"
fi
wait $WF 2>/dev/null; WF=""

echo "== AC#2 idempotent resume: re-run the coordinator on the SAME id =="
REVIEWERS_BEFORE=$(lists | grep -cE "[[:space:]](reviewer|merger)[[:space:]]+agent[[:space:]]")
"$SXWF" --creds "$P/coord.creds" --store "$S" --id wfdemo --spawn-subject msg.topic.spawn --step-timeout 20s >"$P/wf-resume.log" 2>&1
AGENTS_AFTER=$(lists | grep -cE "[[:space:]](reviewer|merger)[[:space:]]+agent[[:space:]]")
if grep -q "workflow wfdemo: done" "$P/wf-resume.log" && [ "$AGENTS_AFTER" -eq "$REVIEWERS_BEFORE" ]; then
  ok "resumed coordinator loaded the checkpoint, skipped done steps, dispatched NOTHING new (AC#2 idempotent resume)"
else
  no "resume re-ran steps or didn't reattach (agents before=$REVIEWERS_BEFORE after=$AGENTS_AFTER)"; cat "$P/wf-resume.log"
fi

echo "== AC#2 cooperative control: a pre-seeded cancel stops a workflow at its safe point =="
pub_as "$P/boss.creds" msg.workflow.wfcancel.control '{"$type":"workflow.control","verb":"cancel"}'  # seeded BEFORE start; DeliverAll replays it
cat >"$P/plan2.json" <<EOF
{"id":"wfcancel","steps":[{"id":"review","kind":"dispatch","nickname":"never","prompt":"should not run"}]}
EOF
"$SXWF" --creds "$P/coord.creds" --store "$S" --plan "$P/plan2.json" --id wfcancel --spawn-subject msg.topic.spawn --step-timeout 20s >"$P/wf-cancel.log" 2>&1
if waitfor '"status":"cancelled"' 'state wfcancel' 15 && ! lists | grep -qE "[[:space:]]never[[:space:]]"; then
  ok "coordinator honoured the cancel control and ran no steps (AC#2 cooperative control)"
else
  no "AC#2 control: workflow not cancelled or it ran a step anyway"; cat "$P/wf-cancel.log"
fi

echo "== result: $PASS passed, $FAIL failed =="
[ "$FAIL" -eq 0 ]
