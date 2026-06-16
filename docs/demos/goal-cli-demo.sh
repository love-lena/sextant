#!/usr/bin/env bash
# One-command, self-validating demo of the `sextant goal` CLI (TASK-84/ADR-0035):
# declare and move a shared objective. `goal set` CAS-upserts the latest-value
# artifact goal.<id> AND signals the transition as a goal.update on
# msg.topic.goals — self-report, the model lena chose for agent.status.
#
# Stages a throwaway bus and ASSERTS the contract (exits non-zero on any failure).
set -uo pipefail

REPO="$(cd "$(dirname "$0")/../.." && pwd)"
D="$(mktemp -d /tmp/goal-cli-demo.XXXXXX)"
say()  { printf '\033[1;36m[demo]\033[0m %s\n' "$*"; }
pass=0 fail=0
check() { if [ "$2" -eq 0 ]; then printf '  \033[1;32m[PASS]\033[0m %s\n' "$1"; pass=$((pass+1)); else printf '  \033[1;31m[FAIL]\033[0m %s\n' "$1"; fail=$((fail+1)); fi; }

BUS_PID=""
trap 'kill "$BUS_PID" 2>/dev/null || true; rm -rf "$D"' EXIT

export SEXTANT_HOME="$D/home"; STORE="$D/store"; mkdir -p "$SEXTANT_HOME" "$STORE"
BIN="$D/bin/sextant"; mkdir -p "$D/bin"
say "build sextant"; (cd "$REPO" && go build -o "$BIN" ./cmd/sextant) || exit 1

say "throwaway bus"
"$BIN" up --store "$STORE" --port 0 >"$D/bus.log" 2>&1 & BUS_PID=$!
for _ in $(seq 1 80); do [ -f "$STORE/bus.json" ] && break; sleep 0.1; done
[ -f "$STORE/bus.json" ] || { say "bus down"; cat "$D/bus.log"; exit 1; }

say "register a client to speak as"
"$BIN" clients register goalbot --kind agent --store "$STORE" --out "$D/goalbot.creds" >/dev/null 2>&1
G() { "$BIN" goal "$@" --store "$STORE" --creds "$D/goalbot.creds"; }
reads() { "$BIN" read msg.topic.goals --since 0 --store "$STORE" --creds "$D/goalbot.creds" 2>/dev/null; }

say "1. declare a goal"
G set v0.4.0 --title "Ship v0.4.0" --state active --headline "4 PRs in review" >"$D/o1" 2>&1
check "goal set (declare) succeeds + says 'declared'" $([ $? -eq 0 ] && grep -q 'declared' "$D/o1"; echo $?)
rec="$(G get v0.4.0 2>/dev/null)"
printf '%s' "$rec" | grep -q '"state":"active"' && printf '%s' "$rec" | grep -q '"title":"Ship v0.4.0"'; check "goal.v0.4.0 artifact holds state=active + the title" $?
reads | grep -q '"\$type":"goal.update"' && reads | grep -q '"goal":"v0.4.0"' && reads | grep -q '"state":"active"'; check "a goal.update(active) was signalled on msg.topic.goals" $?

say "2. move it (no --title) — state changes, title is preserved"
G set v0.4.0 --state blocked --headline "waiting on Lena's merge" >"$D/o2" 2>&1
check "goal set (update) succeeds + says 'updated'" $([ $? -eq 0 ] && grep -q 'updated' "$D/o2"; echo $?)
rec2="$(G get v0.4.0 2>/dev/null)"
printf '%s' "$rec2" | grep -q '"state":"blocked"'; check "state is now blocked" $?
printf '%s' "$rec2" | grep -q '"title":"Ship v0.4.0"'; check "title was preserved across the update (carry-over)" $?
[ "$(reads | grep -c '"\$type":"goal.update"')" -ge 2 ]; check "a second goal.update was signalled" $?

say "3. list shows it"
G list 2>/dev/null | grep -qE 'v0\.4\.0 +\[blocked'; check "goal list shows v0.4.0 [blocked]" $?

say "4. an unknown state is rejected"
G set bad --title x --state nonsense >"$D/o4" 2>&1; rc=$?
[ "$rc" -ne 0 ] && grep -q 'pending | active | blocked | done | dropped' "$D/o4"; check "goal set rejects an unknown --state with the enum" $?

say "5. --no-signal updates the artifact but emits no goal.update"
before="$(reads | grep -c '"\$type":"goal.update"')"
G set v0.4.0 --state done --no-signal >/dev/null 2>&1
after="$(reads | grep -c '"\$type":"goal.update"')"
G get v0.4.0 2>/dev/null | grep -q '"state":"done"' && [ "$before" -eq "$after" ]; check "--no-signal: goal.<id> updated, no new goal.update" $?

echo
if [ "$fail" -ne 0 ]; then say "RESULT: FAIL ($pass passed, $fail failed)"; exit 1; fi
say "RESULT: PASS — sextant goal CLI verified ($pass checks)"
