#!/usr/bin/env bash
# Research-spike workflow — run harness + token-free plumbing demo.
#
# An LLM ORCHESTRATOR drives a research QUESTION to two comparable reports by spawning a
# fresh worker per step (see research-spike-orchestrator.md + research-spike-notes.md).
# It is a close adaptation of the agentic-dev-workflow harness, simplified to a two-step,
# artifact-only pipeline — NO human gate, NO PR/release, NO git worktree mutation (the
# research workers never edit the repo). This script provides:
#
#   research-spike-workflow.sh demo               # token-free: stub orchestrator + stub
#                                                 # workers on a throwaway bus; proves the
#                                                 # harness plumbing (helpers, named-id
#                                                 # registration, the 2-step pipeline shape,
#                                                 # both artifacts produced). Spends no tokens.
#
#   research-spike-workflow.sh run "<question>"   # LIVE: a real claude research worker +
#                                                 # a real codex (gpt-5.5) rewrite worker on
#                                                 # the real bus. The operator drives this.
#                                                 # Produces research-report (claude) and
#                                                 # research-report-gpt5 (gpt-5.5) artifacts.
#
# The orchestration logic lives in the orchestrator's playbook (an LLM), NOT here — this
# is setup + the wf-* helper tools the orchestrator calls.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
MODE="${1:-demo}"
QUESTION="${2:-}"

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

Question: $WF_TASK

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

  # wf-doc <name> <title> — write a `document` artifact <name> whose BODY is read from
  # stdin, under the orchestrator's own creds (create-or-update, CAS). This is how the
  # harness lands a worker's stdout as an artifact WITHOUT depending on that worker calling
  # an MCP tool itself — the proven reviewer-stdout pattern. Used for the codex rewriter,
  # whose tool-calling we don't want to rely on.
  cat >"$bin/wf-doc" <<'EOF'
#!/usr/bin/env sh
name="$1"; title="$2"
body="$(cat)"
bt="$("$WF_BIN/_wf-esc" "$body")"
tt="$("$WF_BIN/_wf-esc" "$title")"
rec="{\"\$type\":\"document\",\"title\":\"$tt\",\"body\":\"$bt\"}"
rev="$("$WF_SEXTANT" artifact get "$name" --json --store "$SEXTANT_STORE" --creds "$WF_ORCH_CREDS" 2>/dev/null \
  | tr -d ' \n' | sed -n 's/.*"[Rr]evision":\([0-9]*\).*/\1/p')"
if [ -z "$rev" ]; then
  "$WF_SEXTANT" artifact create "$name" "$rec" --store "$SEXTANT_STORE" --creds "$WF_ORCH_CREDS" >/dev/null 2>&1
else
  "$WF_SEXTANT" artifact update "$name" "$rec" --rev "$rev" --store "$SEXTANT_STORE" --creds "$WF_ORCH_CREDS" >/dev/null 2>&1
fi
EOF

  # wf-spawn <role> <claude|codex> <prompt-file> — register a fresh NAMED worker identity
  # and run it with least-privilege tools; print its final output to stdout. Research workers
  # never edit the repo, so neither harness gets Edit/Write/Bash. The claude researcher gets
  # web + read + the sextant artifact tools (writes its artifact via MCP); the codex rewriter
  # gets NO tools (it prints its report; the orchestrator lands it via wf-doc).
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
    # gpt-5.5 rewrite worker (proven reviewer-stdout pattern): the orchestrator passes the
    # prior report + question INTO the prompt, and codex OUTPUTS its from-scratch rewrite to
    # stdout. The orchestrator captures that stdout and writes the artifact itself (wf-doc) —
    # we do NOT depend on codex calling an MCP tool to land the artifact. So no MCP config:
    # nothing for codex to call; it just reasons + prints.
    codex exec "$prompt" --model "${WF_CODEX_MODEL:-gpt-5.5}" </dev/null ;;
  *)
    # claude research worker: web research (WebSearch/WebFetch) + Read + the sextant
    # artifact tools. No Edit/Write/Bash — it researches and writes an artifact, nothing else.
    claude -p "$prompt" --model "${WF_CLAUDE_MODEL:-claude-haiku-4-5}" \
      --strict-mcp-config --mcp-config "$mcp" \
      --allowedTools "WebSearch,WebFetch,Read,mcp__sextant__message_publish,mcp__sextant__artifact_get,mcp__sextant__artifact_create,mcp__sextant__artifact_update" \
      --output-format json </dev/null \
      | jq -r '.result // .text // empty' ;;
esac
EOF

  chmod +x "$bin"/wf-* "$bin"/_wf-esc
}

# ============================ DEMO (token-free plumbing) ============================
if [ "$MODE" = demo ]; then
  P="${P:-/tmp/research-spike-workflow-demo}"; S="$P/store"; PORT="${PORT:-4498}"
  SX="${SX:-$P/sextant}"
  PASS=0; FAIL=0
  ok(){ echo "  PASS: $1"; PASS=$((PASS+1)); }
  no(){ echo "  FAIL: $1"; FAIL=$((FAIL+1)); }

  rm -rf "$P"; mkdir -p "$S"
  echo "== build binary =="
  ( cd "$ROOT" && go build -o "$SX" ./clients/sextant-cli ) || { echo "build failed"; exit 2; }

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

  WF_ID="rsdemo"
  export SEXTANT_STORE="$S" WF_ID WF_DM="$DM" WF_TASK="what is the sextant bus?"
  export WF_SEXTANT="$SX" WF_ORCH_CREDS="$P/orch.creds" WF_WORKERS="$P/workers" WF_STATE="$P/progress.tsv"
  export WF_BIN="$P/bin"
  mkdir -p "$WF_WORKERS"

  gen_helpers "$WF_BIN"
  export PATH="$WF_BIN:$PATH"

  # stub worker: registered identity already minted by wf-spawn; emits canned worker OUTPUT
  # the way each live harness does. The two paths differ on purpose, so the stub exercises
  # the REAL live mechanism for each:
  #   - researcher (claude): writes research-report itself via the sextant MCP (live: claude
  #     reliably calls allowed MCP tools). Here the stub mints it via the orchestrator creds
  #     to model "the artifact landed", then prints a one-line summary.
  #   - rewriter (codex): does NOT write any artifact — it PRINTS the rewritten report to
  #     stdout (live: codex tool-calling is not relied upon). The orchestrator step below
  #     captures that stdout and writes research-report-gpt5 via wf-doc. No tokens spent.
  cat >"$P/stub-worker.sh" <<'EOF'
#!/usr/bin/env sh
role="$1"; harness="$2"
"$WF_BIN/wf-event" "worker $role ($harness) ran"
case "$role" in
  researcher)
    body="$("$WF_BIN/_wf-esc" "# Findings: $WF_TASK

Stub research body (claude). Sources: [stub].")"
    rec="{\"\$type\":\"document\",\"title\":\"research report: $WF_TASK\",\"body\":\"$body\"}"
    "$WF_SEXTANT" artifact create research-report "$rec" --store "$SEXTANT_STORE" --creds "$WF_ORCH_CREDS" >/dev/null 2>&1
    echo "research-report written" ;;
  rewriter)
    # codex prints its from-scratch report to stdout; the harness lands it (no MCP call).
    printf '%s\n' "# Findings: $WF_TASK" "" "Stub rewrite body (gpt-5.5). Independent from-scratch version." ;;
  *) echo "$role done" ;;
esac
EOF
  chmod +x "$P/stub-worker.sh"
  export WF_STUB=1 WF_STUB_WORKER="$P/stub-worker.sh"

  echo "== run the stub orchestrator through the 2-step pipeline =="
  reads(){ "$SX" read "$1" --since 0 --store "$S" --creds "$P/orch.creds" 2>/dev/null; }
  lists(){ "$SX" clients list --store "$S" --creds "$P/orch.creds" 2>/dev/null; }
  arti(){ "$SX" artifact get "$1" --json --store "$S" --creds "$P/orch.creds" 2>/dev/null; }

  # step 1: research (claude) -> research-report
  cat >"$P/p-research" <<EOF
Research the question: $WF_TASK. Write your findings to the artifact research-report.
EOF
  wf-progress research running
  wf-spawn researcher claude "$P/p-research" >/dev/null
  wf-progress research done

  # step 2: rewrite (codex/gpt-5.5) -> research-report-gpt5. The orchestrator passes the
  # prior report INTO the prompt and tells codex to OUTPUT only the rewritten report; the
  # harness captures that stdout and writes the artifact itself (wf-doc) — never relying on
  # codex to call an MCP tool. (Live builds the prompt the same way from research-report.)
  cat >"$P/p-rewrite" <<EOF
Question: $WF_TASK

The Claude report (research-report) is below. Rewrite it from scratch as your own
independent version. Output ONLY the rewritten report as your response — do not call any
tool, do not write any file. Just emit the report text.

--- research-report ---
(prior report content here)
EOF
  wf-progress rewrite running
  wf-spawn rewriter codex "$P/p-rewrite" | wf-doc research-report-gpt5 "research report (gpt-5.5): $WF_TASK"
  wf-progress rewrite done

  wf-event "DONE: research-report + research-report-gpt5 ready to compare"

  # assertions on the 2-step plumbing
  lists | grep -qE "[[:space:]]researcher[[:space:]]+agent[[:space:]]" \
    && lists | grep -qE "[[:space:]]rewriter[[:space:]]+agent[[:space:]]" \
    && ok "each step registered a NAMED worker identity on the bus (researcher/rewriter)" \
    || no "named worker identities missing from the directory"
  reads "msg.workflow.$WF_ID.events" | grep -q "worker researcher" \
    && reads "msg.workflow.$WF_ID.events" | grep -q "worker rewriter" \
    && ok "both workers emitted observable events on the workflow stream" || no "worker events missing from the stream"
  arti research-report | grep -q '"document"' \
    && ok "step 1 produced the research-report artifact (claude's report)" || no "research-report artifact missing"
  arti research-report-gpt5 | grep -q '"document"' \
    && ok "step 2 produced the research-report-gpt5 artifact (gpt-5.5's independent report)" || no "research-report-gpt5 artifact missing"
  arti "$WF_ID.run" | grep -q 'rewrite' \
    && ok "progress artifact tracks each step (rewrite recorded)" || no "progress artifact missing steps"
  reads "msg.workflow.$WF_ID.events" | grep -q '"note":"DONE' \
    && ok "workflow emitted DONE (both reports ready to compare)" || no "no DONE event emitted"

  echo
  echo "== result: $PASS passed, $FAIL failed =="
  [ "$FAIL" -eq 0 ]
  exit $?
fi

# ================================ LIVE RUN (operator) ================================
if [ "$MODE" = run ]; then
  [ -n "$QUESTION" ] || { echo "usage: research-spike-workflow.sh run \"<question>\""; exit 2; }
  : "${SEXTANT_STORE:?set SEXTANT_STORE to the live bus store}"
  SX="$(command -v sextant)"; SXMCP="$(command -v sextant-mcp)"
  [ -n "$SX" ] || { echo "sextant not on PATH"; exit 2; }
  command -v claude >/dev/null || { echo "claude not on PATH"; exit 2; }
  command -v codex  >/dev/null || { echo "codex not on PATH"; exit 2; }

  WF_ID="${WF_ID:-rs$(date +%s 2>/dev/null || echo run)}"
  echo "== register the orchestrator (top-level; uses your active context) =="
  WORKERS="${TMPDIR:-/tmp}/sextant-rs/$WF_ID"; mkdir -p "$WORKERS"
  "$SX" clients register "orchestrator-$WF_ID" --kind agent --store "$SEXTANT_STORE" --out "$WORKERS/orch.creds" >/dev/null
  ORCH_ID="$("$SX" clients list --store "$SEXTANT_STORE" --creds "$WORKERS/orch.creds" | awk -v r="orchestrator-$WF_ID" '$0 ~ r {print $1}' | head -1)"
  PRINCIPAL="$("$SX" principal get --store "$SEXTANT_STORE" --creds "$WORKERS/orch.creds" 2>/dev/null | grep -oE '01[0-9A-HJKMNP-TV-Z]{24}' | head -1)"
  [ -n "$PRINCIPAL" ] || { echo "could not read principal"; exit 2; }
  if [ "$PRINCIPAL" \< "$ORCH_ID" ]; then DM="msg.topic.dm.$PRINCIPAL.$ORCH_ID"; else DM="msg.topic.dm.$ORCH_ID.$PRINCIPAL"; fi

  export SEXTANT_STORE WF_ID WF_DM="$DM" WF_TASK="$QUESTION" WF_PRINCIPAL="$PRINCIPAL"
  export WF_SEXTANT="$SX" WF_SEXTANT_MCP="$SXMCP" WF_ORCH_CREDS="$WORKERS/orch.creds" WF_WORKERS="$WORKERS"
  export WF_STATE="$WORKERS/progress.tsv" WF_BIN="$WORKERS/bin"
  gen_helpers "$WF_BIN"; export PATH="$WF_BIN:$PATH"

  export WF_PLAYBOOK="$ROOT/docs/demos/research-spike-orchestrator.md"
  export WF_MCP="$WORKERS/orch.mcp.json"
  export WF_SESSION="$WORKERS/orch.session"
  export WF_TURN1="$WORKERS/turn1.json"
  export WF_ALLOWED="Read,Bash,mcp__sextant__message_publish,mcp__sextant__message_read,mcp__sextant__artifact_create,mcp__sextant__artifact_update,mcp__sextant__artifact_get,mcp__sextant__clients_list"
  : "${WF_ORCH_MODEL:=claude-sonnet-4-6}"; export WF_ORCH_MODEL   # the orchestrator reasons; workers default to haiku/gpt-5.5
  printf '{"mcpServers":{"sextant":{"command":"%s","env":{"SEXTANT_CREDS":"%s","SEXTANT_STORE":"%s"}}}}' \
    "$SXMCP" "$WORKERS/orch.creds" "$SEXTANT_STORE" > "$WF_MCP"
  export WF_PIPELINE="$WORKERS/pipeline.json"
  printf '%s' "${WF_STEPS:-[]}" > "$WF_PIPELINE"   # the def's explicit steps; the orchestrator reads + executes them

  # The orchestrator's turn: claude -p with the playbook as an appended system prompt.
  # No gate, so no spawn-poc wake loop — the supervisor just re-invokes the orchestrator
  # with a "continue" nudge until it emits DONE (the 2-step pipeline may not fit one turn).
  # A QUOTED heredoc — orch-turn.sh reads everything from the exported WF_* env at runtime.
  cat >"$WORKERS/orch-turn.sh" <<'EOF'
#!/usr/bin/env sh
set -u
common="--append-system-prompt-file $WF_PLAYBOOK --mcp-config $WF_MCP --strict-mcp-config --permission-mode acceptEdits --allowedTools $WF_ALLOWED --model $WF_ORCH_MODEL"
if [ -s "$WF_SESSION" ]; then
  # resume turn: the supervisor woke us with $SX_WAKE_TEXT (a "continue" nudge when the
  # prior turn ended mid-pipeline). -s (non-empty), not -f: an empty session would error.
  claude -p "$SX_WAKE_TEXT" --resume "$(cat "$WF_SESSION")" $common --output-format text </dev/null
else
  # first turn: drive the pipeline from the question + the pipeline file; capture the
  # session id (robust jq parse) so subsequent supervisor turns can --resume this orchestrator.
  out="$(claude -p "Question: $WF_TASK. Your pipeline is in the file $WF_PIPELINE - read it first, then execute it step by step per your playbook." $common --output-format json </dev/null)"
  printf '%s' "$out" > "$WF_TURN1"
  printf '%s' "$out" | jq -r '.session_id // empty' > "$WF_SESSION" 2>/dev/null || true
fi
EOF
  chmod +x "$WORKERS/orch-turn.sh"

  echo "== launch the orchestrator under a resilient supervisor loop =="
  echo "   workflow id: $WF_ID   DM: $DM"

  # run_state inspects the workflow's observable state after an orchestrator turn:
  #   done    — a DONE event was emitted (both reports written); stop.
  #   running — the turn ended mid-pipeline (e.g. ran out of turn budget); resume to continue.
  # There is NO gate in the research spike, so the only non-terminal state is "running".
  run_state() {
    "$SX" read "msg.workflow.$WF_ID.events" --since 0 --store "$SEXTANT_STORE" --creds "$WORKERS/orch.creds" 2>/dev/null \
      | grep -q '"note":"DONE' && { echo done; return; }
    echo running
  }

  WAKE=""                              # input for the next turn ("" = first turn, uses the question)
  MAX_TURNS="${WF_MAX_TURNS:-20}"      # safety cap so a confused orchestrator can't loop forever
  turn=0
  while [ "$turn" -lt "$MAX_TURNS" ]; do
    turn=$((turn + 1))
    SX_WAKE_TEXT="$WAKE" "$WORKERS/orch-turn.sh"
    case "$(run_state)" in
      done) echo "supervisor: workflow done after $turn turn(s)"; break ;;
      running)
        echo "supervisor: turn $turn ended mid-pipeline; resuming to continue"
        WAKE="Continue the workflow from where you left off. Re-read $WF_PIPELINE for the steps and the $WF_ID.run artifact for progress, then carry on." ;;
    esac
  done
  [ "$turn" -ge "$MAX_TURNS" ] && echo "supervisor: hit MAX_TURNS=$MAX_TURNS — stopping (possible loop; inspect $WF_ID.run)"
  echo "== done: compare research-report (claude) vs research-report-gpt5 (gpt-5.5) =="
  exit 0
fi

echo "usage: research-spike-workflow.sh (demo | run \"<question>\")"; exit 2
