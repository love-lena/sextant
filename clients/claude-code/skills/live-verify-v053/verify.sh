#!/usr/bin/env bash
# v0.5.3 live-verify — prove the agent runtimes are OPERATIONAL on the operator's
# live brew setup (goal.v0-5-3). The companion to SKILL.md: it runs the AUTOMATED
# criteria directly and prints the operator prompts + outcome checks for the two
# GUIDED ones (Mobilize, workflow). At the end it prints a per-criterion PASS/FAIL.
#
# This runs against the operator's REAL setup: it resolves `sextant` from PATH and
# the live context/store the bus is on. It pins NO test store — that is the point,
# it verifies the thing the operator actually uses.
#
#   verify.sh                 run every automatable check + emit the guided prompts
#   verify.sh <check>         one check: ships|dispatcher|mobilize|workflow|restart|violet
#   verify.sh --self-test     structure/dry-run self-validation — no live bus needed
#   verify.sh --help          this usage
#
# The only mutating action is criterion 5's bus restart, gated behind an explicit
# operator confirmation (warn before killing a live bus).
set -uo pipefail

SX="${SX:-sextant}"                       # live CLI from PATH (override for tests)
RUNTIMES=(sextant-dispatch sextant-violet sextant-workflow)
PASS=0; FAIL=0
declare -a RESULTS=()

c_pass() { PASS=$((PASS + 1)); RESULTS+=("$(printf '  %-2s %-24s PASS  (%s)' "$1" "$2" "$3")"); }
c_fail() { FAIL=$((FAIL + 1)); RESULTS+=("$(printf '  %-2s %-24s FAIL  (%s)' "$1" "$2" "$3")"); }
c_todo() { RESULTS+=("$(printf '  %-2s %-24s GUIDED (%s)' "$1" "$2" "$3")"); }

note()  { printf '\n>> %s\n' "$*"; }
guide() { printf '\n   OPERATOR: %s\n   VERIFY:   %s\n' "$1" "$2"; }

# --- automated checks --------------------------------------------------------

# 1 dispatcher-ships — the three runtimes resolve on PATH (brew installed them).
check_ships() {
	note "[1] dispatcher-ships — runtimes on PATH"
	local missing=() ok=0
	for b in "${RUNTIMES[@]}"; do
		if command -v "$b" >/dev/null 2>&1; then
			ok=$((ok + 1)); printf '   %-18s %s\n' "$b" "$(command -v "$b")"
		else
			missing+=("$b"); printf '   %-18s MISSING\n' "$b"
		fi
	done
	if [ ${#missing[@]} -eq 0 ]; then
		c_pass 1 dispatcher-ships "$ok/3 runtimes on PATH"
	else
		c_fail 1 dispatcher-ships "missing: ${missing[*]} — run \`sextant update\`"
	fi
}

# 2 dispatcher-managed — launchd RUNNING + online on the bus.
check_dispatcher() {
	note "[2] dispatcher-managed — service running + online"
	local st cl run=0 online=0
	st="$("$SX" components status 2>&1)"; printf '%s\n' "$st"
	if printf '%s' "$st" | grep -E 'dispatcher' | grep -qi 'RUNNING'; then run=1; fi
	cl="$("$SX" clients list 2>&1)"
	if printf '%s' "$cl" | grep -qiE 'dispatch.*online|online.*dispatch'; then online=1; fi
	if [ $run -eq 1 ] && [ $online -eq 1 ]; then
		c_pass 2 dispatcher-managed "running + online"
	elif [ $run -ne 1 ]; then
		c_fail 2 dispatcher-managed "not RUNNING — \`sextant components restart dispatcher\`"
	else
		c_fail 2 dispatcher-managed "RUNNING but not online on the bus"
	fi
}

# 6 violet-deployed-online — keyed + running + online (+ DM/FAB are guided).
check_violet() {
	note "[6] violet-deployed-online — keyed, running, online"
	local st cl run=0 online=0
	st="$("$SX" components status 2>&1)"; printf '%s\n' "$st" | grep -E 'violet' || true
	if printf '%s' "$st" | grep -E 'violet' | grep -qi 'RUNNING'; then run=1; fi
	cl="$("$SX" clients list 2>&1)"
	if printf '%s' "$cl" | grep -qiE 'violet.*online|online.*violet'; then online=1; fi
	if [ $run -eq 1 ] && [ $online -eq 1 ]; then
		c_pass 6 violet-deployed-online "running + online — confirm DM reply + FAB next"
		guide "open the dash (\`sextant dash url\`) and use the Assistant FAB" \
			"the FAB responds (not a dead stub); then DM violet and confirm a reply"
	elif [ $run -ne 1 ]; then
		c_fail 6 violet-deployed-online "violet not RUNNING — \`sextant secret set anthropic\` then \`sextant components restart violet\`"
	else
		c_fail 6 violet-deployed-online "RUNNING but not online — check the anthropic key"
	fi
}

# 5 survives-restart — restart the bus, runtimes reconnect on the same port.
check_restart() {
	note "[5] survives-restart — runtimes reconnect after a bus restart"
	note "WARNING: this restarts the live bus — anything connected drops briefly."
	printf '   Restart the bus now? [y/N] '
	local ans; read -r ans
	if [ "${ans:-}" != "y" ] && [ "${ans:-}" != "Y" ]; then
		c_fail 5 survives-restart "skipped — operator declined the restart"
		return
	fi
	local before after
	before="$("$SX" doctor 2>&1 | grep -iE 'url:' | head -1)"
	printf '   port before: %s\n' "${before:-<unknown>}"
	brew services restart sextant >/dev/null 2>&1 || "$SX" up --restart >/dev/null 2>&1 || true
	# wait for the bus + recorded URL to come back (fail-loud, bounded)
	local waited=0
	while [ "$waited" -lt 30 ]; do
		after="$("$SX" doctor 2>&1 | grep -iE 'url:' | head -1)"
		[ -n "$after" ] && [ "$after" = "$before" ] && break
		sleep 1; waited=$((waited + 1))
	done
	printf '   port after:  %s\n' "${after:-<unknown>}"
	local run online
	run="$("$SX" components status 2>&1)"
	online="$("$SX" clients list 2>&1)"
	if [ -n "$after" ] && [ "$after" = "$before" ] \
		&& printf '%s' "$run" | grep -E 'dispatcher' | grep -qi 'RUNNING' \
		&& printf '%s' "$online" | grep -qiE 'dispatch.*online|online.*dispatch'; then
		c_pass 5 survives-restart "port unchanged, dispatcher reconnected"
	else
		c_fail 5 survives-restart "port changed or a runtime did not reconnect — see doctor"
	fi
}

# --- guided checks (the operator clicks; the agent verifies the outcome) ------

guide_mobilize() {
	note "[3] mobilize-end-to-end-live — GUIDED"
	note "Snapshot the directory, then have the operator click Mobilize."
	"$SX" clients list 2>&1 | sed 's/^/   before: /'
	guide "open the dash (\`sextant dash url\`) and click Mobilize" \
		"a NEW client (kind agent) appears online in \`sextant clients list\`, then DM it and confirm a reply"
	c_todo 3 mobilize-end-to-end-live "operator clicks Mobilize; agent verifies new agent online + DM reply"
}

guide_workflow() {
	note "[4] workflow-run-live — GUIDED"
	guide "open the dash Workflow page and Start a workflow from a prompt" \
		"the start is ack'd fast, then the run moves running→done (workflow.<id> events / the run view) — NOT a 'no runner' timeout"
	c_todo 4 workflow-run-live "operator starts a workflow; agent verifies it reaches done"
}

# --- self-test: no live bus needed -------------------------------------------

self_test() {
	local ok=1
	# the SKILL.md sits next to this script and names every criterion
	local dir; dir="$(cd "$(dirname "$0")" && pwd)"
	local skill="$dir/SKILL.md"
	[ -f "$skill" ] || { echo "FAIL: SKILL.md missing next to verify.sh"; ok=0; }
	for crit in dispatcher-ships dispatcher-managed mobilize-end-to-end-live \
		workflow-run-live survives-restart violet-deployed-online; do
		grep -q "$crit" "$skill" 2>/dev/null || { echo "FAIL: SKILL.md missing criterion $crit"; ok=0; }
	done
	# the script defines a check fn per criterion
	for fn in check_ships check_dispatcher check_violet check_restart guide_mobilize guide_workflow; do
		declare -F "$fn" >/dev/null || { echo "FAIL: missing function $fn"; ok=0; }
	done
	# the three runtimes are the v0.5.3 set
	[ "${RUNTIMES[*]}" = "sextant-dispatch sextant-violet sextant-workflow" ] \
		|| { echo "FAIL: runtime set drifted: ${RUNTIMES[*]}"; ok=0; }
	if [ $ok -eq 1 ]; then echo "self-test: PASS — skill structure + 6 criteria + runner functions intact"; return 0; fi
	echo "self-test: FAIL"; return 1
}

# report prints the per-criterion lines + a verdict. full=1 means every criterion
# was attempted, so a clean run can declare goal.v0-5-3 met; full=0 (a single
# check) reports only that check's result.
report() {
	local full="${1:-0}"
	printf '\nv0.5.3 live-verify — goal.v0-5-3\n'
	printf '%s\n' "${RESULTS[@]}"
	local guided ran; guided=$(printf '%s\n' "${RESULTS[@]}" | grep -c 'GUIDED'); ran=${#RESULTS[@]}
	if [ "$full" = 1 ] && [ "$FAIL" -eq 0 ] && [ "$guided" -eq 0 ]; then
		printf '  -> %d/6 green: goal.v0-5-3 met\n' "$PASS"
	else
		printf '  -> %d check(s): %d pass / %d fail / %d guided (await operator action + agent verification)\n' \
			"$ran" "$PASS" "$FAIL" "$guided"
	fi
	[ "$FAIL" -eq 0 ]
}

main() {
	case "${1:---all}" in
	--help | -h)
		grep -E '^#( |$)' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	--self-test) self_test; exit $? ;;
	ships)       check_ships;      report 0; exit $? ;;
	dispatcher)  check_dispatcher; report 0; exit $? ;;
	mobilize)    guide_mobilize;   report 0; exit 0 ;;
	workflow)    guide_workflow;   report 0; exit 0 ;;
	restart)     check_restart;    report 0; exit $? ;;
	violet)      check_violet;     report 0; exit $? ;;
	--all)
		check_ships
		check_dispatcher
		guide_mobilize
		guide_workflow
		# criterion 5 (restart) is invoked explicitly — it is disruptive
		note "[5] survives-restart is disruptive — run \`verify.sh restart\` when ready"
		c_todo 5 survives-restart "run \`verify.sh restart\` (restarts the live bus, gated on confirm)"
		check_violet
		report 1; exit $? ;;
	*)
		echo "unknown check: $1 (try --help)"; exit 2 ;;
	esac
}

main "$@"
