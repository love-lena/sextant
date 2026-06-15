#!/usr/bin/env bash
# Agentic dev workflow — run harness + token-free plumbing demo (TASK-97).
#
# An LLM ORCHESTRATOR drives a task to an open PR by spawning a fresh worker per step
# and resuming at each handoff (see agentic-dev-workflow-orchestrator.md +
# agentic-dev-workflow-notes.md). This script provides:
#
#   agentic-dev-workflow.sh demo            # token-free: stub orchestrator + stub
#                                           # workers on a throwaway bus + repo; proves
#                                           # the harness plumbing (helpers, named-id
#                                           # registration, the spawn-poc gate round-trip,
#                                           # the open-PR path). Spends no model tokens.
#
#   agentic-dev-workflow.sh run "<task>"    # LIVE: real claude/codex workers on the real
#                                           # bus + a real sextant worktree. The operator
#                                           # drives this (the safety classifier blocks an
#                                           # unattended agent from launching autonomous
#                                           # editing agents). Opens a PR; never merges.
#
# The orchestration logic lives in the orchestrator's playbook (an LLM), NOT here — this
# is setup + the wf-* helper tools the orchestrator calls.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
MODE="${1:-demo}"
TASK="${2:-}"

# --- shared helper-script generation -----------------------------------------------
# The orchestrator's Bash calls these by name; they read the WF_* env exported below.
# Generated into $WF_BIN, which the harness puts on PATH.
gen_helpers() {
  local bin="$1"
  mkdir -p "$bin"

  # JSON-string escaper shared by the publishers: backslash, double-quote, newline.
  cat >"$bin/_wf-esc" <<'EOF'
#!/usr/bin/env sh
# JSON-string-escape stdin's single arg: backslash, quote, then control chars.
# perl (-0777 slurps, so multi-line bodies survive) is robust on macOS + Linux —
# the sed ':a;N' newline-join idiom silently drops a final line with no trailing
# newline on BSD/macOS sed.
printf '%s' "$1" | perl -0777 -pe 's/\\/\\\\/g; s/"/\\"/g; s/\n/\\n/g; s/\r/\\r/g; s/\t/\\t/g'
EOF

  # wf-event "<text>" — a human-readable line on the workflow event stream.
  cat >"$bin/wf-event" <<'EOF'
#!/usr/bin/env sh
t="$("$WF_BIN/_wf-esc" "$1")"
"$WF_SEXTANT" publish "msg.workflow.$WF_ID.events" \
  "{\"\$type\":\"workflow.event\",\"status\":\"note\",\"note\":\"$t\"}" \
  --creds "$WF_ORCH_CREDS" --store "$SEXTANT_STORE" >/dev/null 2>&1
EOF

  # wf-dm "<text>" — DM the principal (headline only).
  cat >"$bin/wf-dm" <<'EOF'
#!/usr/bin/env sh
t="$("$WF_BIN/_wf-esc" "$1")"
"$WF_SEXTANT" publish "$WF_DM" \
  "{\"\$type\":\"chat.message\",\"text\":\"$t\"}" \
  --creds "$WF_ORCH_CREDS" --store "$SEXTANT_STORE" >/dev/null 2>&1
EOF

  # wf-progress <step> <status> [verdict] — update the progress artifact $WF_ID. Keeps a
  # local state file and republishes the whole doc (create-or-update, CAS via the CLI).
  cat >"$bin/wf-progress" <<'EOF'
#!/usr/bin/env sh
step="$1"; status="$2"; verdict="${3:-}"
line="$step	$status	$verdict"
touch "$WF_STATE"
# replace any prior line for this step, then append the new one.
grep -v "^$step	" "$WF_STATE" > "$WF_STATE.tmp" 2>/dev/null || true
mv "$WF_STATE.tmp" "$WF_STATE"
printf '%s\n' "$line" >> "$WF_STATE"
body="# Workflow $WF_ID

Task: $WF_TASK

| step | status | verdict |
|------|--------|---------|
"
while IFS='	' read -r s st vd; do
  body="$body| $s | $st | $vd |
"
done < "$WF_STATE"
bt="$("$WF_BIN/_wf-esc" "$body")"
rec="{\"\$type\":\"document\",\"title\":\"workflow $WF_ID\",\"body\":\"$bt\"}"
# create on first call, else CAS-update on the current revision.
rev="$("$WF_SEXTANT" artifact get "$WF_ID.run" --json --store "$SEXTANT_STORE" --creds "$WF_ORCH_CREDS" 2>/dev/null \
  | tr -d ' \n' | sed -n 's/.*"[Rr]evision":\([0-9]*\).*/\1/p')"
if [ -z "$rev" ]; then
  "$WF_SEXTANT" artifact create "$WF_ID.run" "$rec" --store "$SEXTANT_STORE" --creds "$WF_ORCH_CREDS" >/dev/null 2>&1
else
  "$WF_SEXTANT" artifact update "$WF_ID.run" "$rec" --rev "$rev" --store "$SEXTANT_STORE" --creds "$WF_ORCH_CREDS" >/dev/null 2>&1
fi
EOF

  # wf-spawn <role> <claude|codex> <prompt-file> — register a fresh NAMED worker identity
  # and run it with least-privilege tools scoped to the worktree; print its final output.
  cat >"$bin/wf-spawn" <<'EOF'
#!/usr/bin/env sh
role="$1"; harness="$2"; promptfile="$3"
creds="$WF_WORKERS/$role.creds"
if [ ! -f "$creds" ]; then
  "$WF_SEXTANT" clients register "$role" --kind agent --store "$SEXTANT_STORE" \
    --out "$creds" >/dev/null 2>&1
fi
if [ -n "${WF_STUB:-}" ]; then
  exec "$WF_STUB_WORKER" "$role" "$harness" "$promptfile"
fi
mcp="$WF_WORKERS/$role.mcp.json"
printf '{"mcpServers":{"sextant":{"command":"%s","env":{"SEXTANT_CREDS":"%s","SEXTANT_STORE":"%s"}}}}' \
  "$WF_SEXTANT_MCP" "$creds" "$SEXTANT_STORE" > "$mcp"
prompt="$(cat "$promptfile")"
case "$harness" in
  codex)
    # read-only reviewer.
    codex exec "$prompt" --model "${WF_CODEX_MODEL:-gpt-5.5}" \
      -c "mcp_servers.sextant.command=$WF_SEXTANT_MCP" \
      -c "mcp_servers.sextant.env.SEXTANT_CREDS=$creds" \
      -c "mcp_servers.sextant.env.SEXTANT_STORE=$SEXTANT_STORE" </dev/null ;;
  *)
    # claude worker: edit+bash scoped to the worktree.
    claude -p "$prompt" --model "${WF_CLAUDE_MODEL:-claude-haiku-4-5}" \
      --strict-mcp-config --mcp-config "$mcp" --add-dir "$WF_WORKTREE" \
      --permission-mode acceptEdits \
      --allowedTools "Read,Edit,Write,Bash,mcp__sextant__message_publish,mcp__sextant__artifact_create,mcp__sextant__artifact_update" \
      --output-format text </dev/null ;;
esac
EOF

  # wf-spawn-resume <role> <prompt-file> — RESUME the same role worker (sticky fixer).
  cat >"$bin/wf-spawn-resume" <<'EOF'
#!/usr/bin/env sh
role="$1"; promptfile="$2"
if [ -n "${WF_STUB:-}" ]; then
  exec "$WF_STUB_WORKER" "$role" resume "$promptfile"
fi
sid="$WF_WORKERS/$role.session"
mcp="$WF_WORKERS/$role.mcp.json"
prompt="$(cat "$promptfile")"
# Resume the prior claude session if we captured one, else fall back to a fresh turn.
if [ -f "$sid" ]; then
  claude -p "$prompt" --resume "$(cat "$sid")" --model "${WF_CLAUDE_MODEL:-claude-haiku-4-5}" \
    --strict-mcp-config --mcp-config "$mcp" --add-dir "$WF_WORKTREE" \
    --permission-mode acceptEdits \
    --allowedTools "Read,Edit,Write,Bash,mcp__sextant__message_publish,mcp__sextant__artifact_create,mcp__sextant__artifact_update" \
    --output-format text </dev/null
else
  "$WF_BIN/wf-spawn" "$role" claude "$promptfile"
fi
EOF

  chmod +x "$bin"/wf-* "$bin"/_wf-esc
}

# ============================ DEMO (token-free plumbing) ============================
if [ "$MODE" = demo ]; then
  P="${P:-/tmp/agentic-dev-workflow-demo}"; S="$P/store"; PORT="${PORT:-4497}"
  SX="${SX:-$P/sextant}"; SXPOC="${SXPOC:-$P/spawn-poc}"
  PASS=0; FAIL=0
  ok(){ echo "  PASS: $1"; PASS=$((PASS+1)); }
  no(){ echo "  FAIL: $1"; FAIL=$((FAIL+1)); }

  rm -rf "$P"; mkdir -p "$S"
  echo "== build binaries =="
  ( cd "$ROOT" && go build -o "$SX" ./cmd/sextant && go build -o "$SXPOC" ./cmd/spawn-poc ) || { echo "build failed"; exit 2; }

  echo "== throwaway bus on :$PORT =="
  "$SX" up --store "$S" --port "$PORT" >"$P/up.log" 2>&1 & BUS=$!
  trap 'kill $BUS 2>/dev/null' EXIT
  for _ in $(seq 1 100); do [ -f "$S/bus.json" ] && break; sleep 0.1; done
  [ -f "$S/bus.json" ] || { echo "bus didn't start"; exit 2; }

  # the principal + the orchestrator identities.
  "$SX" clients register boss --kind human --store "$S" --out "$P/boss.creds" >/dev/null 2>&1
  "$SX" clients register orchestrator --kind agent --store "$S" --out "$P/orch.creds" >/dev/null 2>&1
  BOSS_ID="$("$SX" clients list --store "$S" --creds "$P/orch.creds" | awk '/ boss /{print $1}')"
  ORCH_ID="$("$SX" clients list --store "$S" --creds "$P/orch.creds" | awk '/ orchestrator /{print $1}')"
  # DM subject = msg.topic.dm.<sorted ids>.
  if [ "$BOSS_ID" \< "$ORCH_ID" ]; then DM="msg.topic.dm.$BOSS_ID.$ORCH_ID"; else DM="msg.topic.dm.$ORCH_ID.$BOSS_ID"; fi

  WF_ID="wfdemo"
  export SEXTANT_STORE="$S" WF_ID WF_DM="$DM" WF_TASK="add a hello flag" WF_WORKTREE="$P/wt"
  export WF_SEXTANT="$SX" WF_ORCH_CREDS="$P/orch.creds" WF_WORKERS="$P/workers" WF_STATE="$P/progress.tsv"
  export WF_BIN="$P/bin"
  mkdir -p "$WF_WORKERS" "$WF_WORKTREE"

  gen_helpers "$WF_BIN"
  export PATH="$WF_BIN:$PATH"

  # stub worker: registered identity already minted by wf-spawn; here we just emit the
  # canned output the orchestrator reads. The reviewer returns changes-requested once,
  # then approved (proving the bounded loop + early-exit).
  cat >"$P/stub-worker.sh" <<'EOF'
#!/usr/bin/env sh
role="$1"; harness="$2"
"$WF_BIN/wf-event" "worker $role ($harness) ran"
case "$role" in
  reviewer)
    c="$WF_WORKERS/.reviewer.round"; n=$(( $(cat "$c" 2>/dev/null || echo 0) + 1 )); echo "$n" > "$c"
    if [ "$n" -lt 2 ]; then echo "needs a tweak"; echo "VERDICT: changes-requested";
    else echo "looks good"; echo "VERDICT: approved"; fi ;;
  *) echo "$role done" ;;
esac
EOF
  chmod +x "$P/stub-worker.sh"
  export WF_STUB=1 WF_STUB_WORKER="$P/stub-worker.sh"

  echo "== run the stub orchestrator through the pre-gate pipeline =="
  reads(){ "$SX" read "$1" --since 0 --store "$S" --creds "$P/orch.creds" 2>/dev/null; }
  lists(){ "$SX" clients list --store "$S" --creds "$P/orch.creds" 2>/dev/null; }

  # plan -> implement
  echo "plan it" > "$P/pp"; wf-progress plan running; wf-spawn planner claude "$P/pp" >/dev/null; wf-progress plan done
  echo "build it" > "$P/pi"; wf-progress implement running; wf-spawn implementer claude "$P/pi" >/dev/null; wf-progress implement done
  # review<->fix loop (bounded 3)
  round=0; verdict=""
  while [ "$round" -lt 3 ]; do
    round=$((round+1))
    echo "review the diff" > "$P/pr"
    out="$(wf-spawn reviewer codex "$P/pr")"
    verdict="$(printf '%s\n' "$out" | sed -n 's/^VERDICT: //p' | tail -1)"
    wf-progress review "round-$round" "$verdict"
    [ "$verdict" = approved ] && break
    echo "fix per: $out" > "$P/pf"
    if [ "$round" -eq 1 ]; then wf-spawn fixer claude "$P/pf" >/dev/null; else wf-spawn-resume fixer "$P/pf" >/dev/null; fi
    wf-progress fix "round-$round" done
  done
  [ "$verdict" = approved ] && ok "review<->fix loop reached approved (round $round) and exited bounded" || no "loop did not converge (verdict=$verdict)"
  echo "write the brief" > "$P/pb"; wf-progress brief running; wf-spawn briefer claude "$P/pb" >/dev/null; wf-progress brief done

  # assertions on the pre-gate plumbing
  lists | grep -qE "[[:space:]]planner[[:space:]]+agent[[:space:]]" && lists | grep -qE "[[:space:]]implementer[[:space:]]+agent[[:space:]]" \
    && lists | grep -qE "[[:space:]]reviewer[[:space:]]+agent[[:space:]]" && lists | grep -qE "[[:space:]]fixer[[:space:]]+agent[[:space:]]" \
    && ok "each step registered a NAMED worker identity on the bus (planner/implementer/reviewer/fixer/briefer)" \
    || no "named worker identities missing from the directory"
  reads "msg.workflow.$WF_ID.events" | grep -q "worker reviewer" && ok "workers emitted observable events on the workflow stream" || no "no worker events on the stream"
  "$SX" artifact get "$WF_ID.run" --json --store "$S" --creds "$P/orch.creds" 2>/dev/null | grep -q 'brief' \
    && ok "progress artifact tracks each step (brief recorded)" || no "progress artifact missing steps"

  echo "== human GATE via spawn-poc: post gate, yield, resume on a seeded approve =="
  # the resume adapter = what spawn-poc re-invokes when a control lands; it does release.
  cat >"$P/orch-resume.sh" <<'EOF'
#!/usr/bin/env sh
# $SX_WAKE_TEXT carries the control message that woke us.
echo "$SX_WAKE_TEXT" | grep -q approve || { "$WF_BIN/wf-event" "woke on non-approve: $SX_WAKE_TEXT"; exit 0; }
"$WF_BIN/wf-progress" release running
# release = open a PR (stubbed here; the live run does gh pr create). Prove the path:
"$WF_BIN/wf-event" "release: would run: gh pr create (open PR, no merge)"
touch "$WF_PR_MARKER"
"$WF_BIN/wf-progress" release done
"$WF_BIN/wf-dm" "PR opened (stub): workflow $WF_ID done"
EOF
  chmod +x "$P/orch-resume.sh"
  export WF_PR_MARKER="$P/pr-opened"

  wf-progress gate awaiting-approval
  wf-dm "workflow $WF_ID ready for review — reply approve on msg.workflow.$WF_ID.control"
  # supervisor watches the control subject; wakes orch-resume on a control message.
  "$SXPOC" --creds "$P/orch.creds" --store "$S" --agent "$ORCH_ID" \
    --watch "msg.workflow.$WF_ID.control" --on-wake "$P/orch-resume.sh" \
    --deadline 30s --wake-timeout 30s >"$P/poc.log" 2>&1 & POC=$!
  sleep 1
  # the principal approves.
  "$SX" publish "msg.workflow.$WF_ID.control" '{"$type":"workflow.control","verb":"approve"}' --creds "$P/boss.creds" --store "$S" >/dev/null 2>&1
  # wait for the release marker.
  for _ in $(seq 1 60); do [ -f "$WF_PR_MARKER" ] && break; sleep 0.25; done
  kill $POC 2>/dev/null
  [ -f "$WF_PR_MARKER" ] && ok "gate→approve→resume→release round-trip worked via spawn-poc (the live gate wiring)" || { no "gate resume never released"; echo "--- poc.log ---"; tail -15 "$P/poc.log"; }
  "$SX" artifact get "$WF_ID.run" --json --store "$S" --creds "$P/orch.creds" 2>/dev/null | grep -q 'release.*done\|done.*release' \
    && ok "progress artifact shows release done" || no "release not marked done in progress"

  echo
  echo "== result: $PASS passed, $FAIL failed =="
  [ "$FAIL" -eq 0 ]
  exit $?
fi

# ================================ LIVE RUN (operator) ================================
if [ "$MODE" = run ]; then
  [ -n "$TASK" ] || { echo "usage: agentic-dev-workflow.sh run \"<task>\""; exit 2; }
  : "${SEXTANT_STORE:?set SEXTANT_STORE to the live bus store}"
  SX="$(command -v sextant)"; SXMCP="$(command -v sextant-mcp)"; SXPOC="${SXPOC:-}"
  [ -n "$SX" ] || { echo "sextant not on PATH"; exit 2; }
  command -v claude >/dev/null || { echo "claude not on PATH"; exit 2; }
  command -v codex  >/dev/null || { echo "codex not on PATH"; exit 2; }
  if [ -z "$SXPOC" ]; then ( cd "$ROOT" && go build -o /tmp/spawn-poc ./cmd/spawn-poc ) && SXPOC=/tmp/spawn-poc; fi

  WF_ID="${WF_ID:-wf$(date +%s 2>/dev/null || echo run)}"
  WT="$ROOT/.claude/worktrees/$WF_ID"
  echo "== isolated worktree + branch =="
  git -C "$ROOT" worktree add "$WT" -b "agentic/$WF_ID" "${WF_BASE:-origin/main}" || { echo "worktree add failed"; exit 2; }

  echo "== register the orchestrator (top-level; uses your active context) =="
  # OUTSIDE the worktree, so a worker's `git add -A` can never stage the orchestrator's
  # creds/scratch into the branch (and thence a public PR).
  WORKERS="${TMPDIR:-/tmp}/sextant-wf/$WF_ID"; mkdir -p "$WORKERS"
  "$SX" clients register "orchestrator-$WF_ID" --kind agent --store "$SEXTANT_STORE" --out "$WORKERS/orch.creds" >/dev/null
  ORCH_ID="$("$SX" clients list --store "$SEXTANT_STORE" --creds "$WORKERS/orch.creds" | awk -v r="orchestrator-$WF_ID" '$0 ~ r {print $1}' | head -1)"
  PRINCIPAL="$("$SX" principal get --store "$SEXTANT_STORE" --creds "$WORKERS/orch.creds" 2>/dev/null | grep -oE '01[0-9A-HJKMNP-TV-Z]{24}' | head -1)"
  [ -n "$PRINCIPAL" ] || { echo "could not read principal"; exit 2; }
  if [ "$PRINCIPAL" \< "$ORCH_ID" ]; then DM="msg.topic.dm.$PRINCIPAL.$ORCH_ID"; else DM="msg.topic.dm.$ORCH_ID.$PRINCIPAL"; fi

  export SEXTANT_STORE WF_ID WF_DM="$DM" WF_TASK="$TASK" WF_WORKTREE="$WT" WF_PRINCIPAL="$PRINCIPAL"
  export WF_SEXTANT="$SX" WF_SEXTANT_MCP="$SXMCP" WF_ORCH_CREDS="$WORKERS/orch.creds" WF_WORKERS="$WORKERS"
  export WF_STATE="$WORKERS/progress.tsv" WF_BIN="$WORKERS/bin"
  gen_helpers "$WF_BIN"; export PATH="$WF_BIN:$PATH"

  export WF_PLAYBOOK="$ROOT/docs/demos/agentic-dev-workflow-orchestrator.md"
  export WF_MCP="$WORKERS/orch.mcp.json"
  export WF_SESSION="$WORKERS/orch.session"
  export WF_TURN1="$WORKERS/turn1.json"
  export WF_ALLOWED="Read,Edit,Write,Bash,mcp__sextant__message_publish,mcp__sextant__message_read,mcp__sextant__artifact_create,mcp__sextant__artifact_update,mcp__sextant__artifact_get,mcp__sextant__clients_list"
  : "${WF_ORCH_MODEL:=claude-sonnet-4-6}"; export WF_ORCH_MODEL   # the orchestrator reasons; workers default to haiku
  printf '{"mcpServers":{"sextant":{"command":"%s","env":{"SEXTANT_CREDS":"%s","SEXTANT_STORE":"%s"}}}}' \
    "$SXMCP" "$WORKERS/orch.creds" "$SEXTANT_STORE" > "$WF_MCP"
  export WF_PIPELINE="$WORKERS/pipeline.json"
  printf '%s' "${WF_STEPS:-[]}" > "$WF_PIPELINE"   # the def's explicit steps; the orchestrator reads + executes them

  # The orchestrator's turn: claude -p with the playbook as an appended system prompt;
  # resumed by spawn-poc on a control message at the gate. A QUOTED heredoc — orch-turn.sh
  # reads everything from the exported WF_* env at runtime (no fragile interpolation of
  # the playbook's quotes/backticks). --append-system-prompt-file passes the playbook by
  # path, so its content never has to survive shell quoting.
  cat >"$WORKERS/orch-turn.sh" <<'EOF'
#!/usr/bin/env sh
set -u
common="--append-system-prompt-file $WF_PLAYBOOK --mcp-config $WF_MCP --strict-mcp-config --add-dir $WF_WORKTREE --permission-mode acceptEdits --allowedTools $WF_ALLOWED --model $WF_ORCH_MODEL"
if [ -f "$WF_SESSION" ]; then
  # a control message woke us at the gate; resume with its text.
  claude -p "$SX_WAKE_TEXT" --resume "$(cat "$WF_SESSION")" $common --output-format text </dev/null
else
  out="$(claude -p "Task: $WF_TASK. Your pipeline is in the file $WF_PIPELINE - read it first, then execute it step by step per your playbook." $common --output-format json </dev/null)"
  printf '%s' "$out" > "$WF_TURN1"
  printf '%s' "$out" | grep -oE '"session_id":"[^"]+"' | head -1 | cut -d'"' -f4 > "$WF_SESSION" 2>/dev/null || true
fi
EOF
  chmod +x "$WORKERS/orch-turn.sh"

  echo "== launch the orchestrator (first turn drives to the gate; spawn-poc resumes it on your control) =="
  echo "   workflow id: $WF_ID   worktree: $WT   DM: $DM"
  "$WORKERS/orch-turn.sh"
  echo "== orchestrator yielded; supervising the gate (reply approve / changes <feedback> on msg.workflow.$WF_ID.control) =="
  exec "$SXPOC" --creds "$WORKERS/orch.creds" --store "$SEXTANT_STORE" --agent "$ORCH_ID" \
    --watch "msg.workflow.$WF_ID.control" --on-wake "$WORKERS/orch-turn.sh" --deadline 24h
fi

echo "usage: agentic-dev-workflow.sh (demo | run \"<task>\")"; exit 2
