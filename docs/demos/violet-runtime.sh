#!/usr/bin/env bash
# violet-runtime.sh — run violet, the operator's assistant, as a long-lived bus
# client (ADR-0039). violet is a CLIENT LIKE ANY OTHER, driven by a role prompt
# (violet-runtime.md) + the violet-curation skill — the SAME lighter shape the
# crew run (a claude-code agent connected via the sextant plugin/MCP), NOT a
# heavy bespoke Go program. This mirrors the agentic-dev-workflow orchestrator
# precedent: a markdown role prompt appended to `claude -p`, woken by a
# supervisor loop.
#
# violet has two duties (both bounded by signal-not-manage — she answers and
# curates a projection, she never acts on the operator's behalf):
#   1. ANSWER (read-only) — woken by a DM from the operator on VL_DM; reads the
#      workspace and replies on VL_DM.
#   2. DEFEND — woken by a periodic TICK (and opportunistically by bus activity);
#      re-curates the operator's `home` projection per the violet-curation skill.
#
# The defend TICK + the answer wake are unified in ONE supervisor loop: each
# iteration waits for the next trigger — either a NEW operator DM (polled via a
# DM message-count cursor, so history is never replayed) OR a periodic timeout —
# then runs one violet turn, resuming the same session so violet carries context
# across turns. (Same supervisor shape as agentic-dev-workflow's gate loop.)
#
#   demo  — self-validating run on a THROWAWAY hermetic bus (default; CI-safe).
#           Stubs `claude` so the loop is validated without a live LLM/live bus.
#   live  — run violet against a real bus. RELEASE-TIME ONLY (ADR-0039 "When it
#           goes live"): needs the operator's sign-off + the `assistant`
#           designation artifact. Documented here; not exercised in CI.
#
# Usage:
#   docs/demos/violet-runtime.sh demo
#   SEXTANT_STORE=<live-store> docs/demos/violet-runtime.sh live   # release-time
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
MODE="${1:-demo}"

# ---- the violet runtime: register, configure, and run violet's turns ----------
# Shared by demo (hermetic) and live. Expects these set:
#   SEXTANT_STORE, VL_SEXTANT (sextant CLI), VL_SEXTANT_MCP (sextant-mcp),
#   VL_CREDS (violet's creds), VL_SELF (violet id), VL_OPERATOR (principal id),
#   VL_DM (operator DM subject), VL_ROLE (role-prompt path), VL_WORK (scratch dir).
# Optional: VL_TICK (seconds between defend ticks; default 900 = 15m),
#   VL_MAX_TURNS (safety cap), VL_CLAUDE (the `claude` binary; stubbed in demo).
run_violet() {
  : "${SEXTANT_STORE:?}"; : "${VL_CREDS:?}"; : "${VL_SELF:?}"; : "${VL_OPERATOR:?}"
  : "${VL_DM:?}"; : "${VL_ROLE:?}"; : "${VL_WORK:?}"
  local TICK="${VL_TICK:-900}" MAX="${VL_MAX_TURNS:-1000}" CLAUDE="${VL_CLAUDE:-claude}"
  local SX="${VL_SEXTANT:-sextant}"
  local MCP="$VL_WORK/violet.mcp.json" SESSION="$VL_WORK/violet.session"

  # The violet-curation skill must be discoverable by `claude` so the DEFEND duty
  # can load it. The plugin ships it under clients/claude-code/skills/; --add-dir
  # the plugin skills dir (and the repo root, so violet can Read design artifacts).
  local SKILLS_DIR="$ROOT/clients/claude-code/skills"

  # MCP config: violet talks to the bus through the sextant MCP under its OWN creds.
  printf '{"mcpServers":{"sextant":{"command":"%s","env":{"SEXTANT_CREDS":"%s","SEXTANT_STORE":"%s"}}}}' \
    "${VL_SEXTANT_MCP:?}" "$VL_CREDS" "$SEXTANT_STORE" > "$MCP"

  local ALLOWED="Read,mcp__sextant__message_publish,mcp__sextant__message_read,mcp__sextant__message_subscribe,mcp__sextant__artifact_get,mcp__sextant__artifact_list,mcp__sextant__artifact_create,mcp__sextant__artifact_update,mcp__sextant__clients_list"
  local MODEL="${VL_MODEL:-claude-sonnet-4-6}"
  local common="--append-system-prompt-file $VL_ROLE --mcp-config $MCP --strict-mcp-config --add-dir $SKILLS_DIR --add-dir $ROOT --permission-mode acceptEdits --allowedTools $ALLOWED --model $MODEL"

  # one violet turn: first turn starts fresh + captures the session id; later turns
  # --resume so violet carries context (her last curation pass, the conversation).
  violet_turn() {
    local wake_text="$1" wake_from="$2"
    if [ -s "$SESSION" ]; then
      VL_WAKE_TEXT="$wake_text" VL_WAKE_FROM="$wake_from" \
        "$CLAUDE" -p "$wake_text" --resume "$(cat "$SESSION")" $common --output-format text </dev/null
    else
      local out
      out="$(VL_WAKE_TEXT="$wake_text" VL_WAKE_FROM="$wake_from" \
        "$CLAUDE" -p "Orient as violet, then run one defend pass per your role prompt." \
        $common --output-format json </dev/null)"
      printf '%s' "$out"
      printf '%s' "$out" | (command -v jq >/dev/null && jq -r '.session_id // empty' || cat) > "$SESSION" 2>/dev/null || true
    fi
  }

  # dm_count — how many messages are on violet's DM right now. The supervisor keeps
  # a cursor and treats a growth as "a new operator message arrived." (Reading the
  # whole DM is fine at assistant scale; a production runtime would page from a
  # stored sequence — the mechanism is the same.)
  dm_count() { "$SX" read "$VL_DM" --since 0 --json --store "$SEXTANT_STORE" --creds "$VL_CREDS" 2>/dev/null | grep -c '"kind": "message"' || true; }

  # wait_for_trigger blocks until EITHER a NEW operator message lands on violet's DM
  # OR the tick elapses — then returns via $WAKE_TEXT/$WAKE_FROM: "__TICK__"/"" on a
  # timeout (the periodic DEFEND tick), else the new message's text + author (an
  # ANSWER wake). It polls a DM message-count cursor (DM_SEEN), so it reacts only to
  # NEW traffic, never replays history. This unifies the DEFEND tick and the ANSWER
  # wake in ONE loop — the same supervisor shape agentic-dev-workflow uses.
  WAKE_TEXT=""; WAKE_FROM=""; DM_SEEN="$(dm_count)"
  wait_for_trigger() {
    local waited=0 step=1 now frame
    while [ "$waited" -lt "$TICK" ]; do
      now="$(dm_count)"
      if [ "${now:-0}" -gt "${DM_SEEN:-0}" ]; then
        DM_SEEN="$now"
        # a new message arrived — pull the latest frame's author + text.
        frame="$("$SX" read "$VL_DM" --since 0 --json --store "$SEXTANT_STORE" --creds "$VL_CREDS" 2>/dev/null || true)"
        WAKE_FROM="$(printf '%s' "$frame" | grep '"author"' | tail -1 | sed -E 's/.*"author": *"([^"]*)".*/\1/' || true)"
        WAKE_TEXT="$(printf '%s' "$frame" | grep '"text"'   | tail -1 | sed -E 's/.*"text": *"(.*)".*/\1/'   || true)"
        if [ "$WAKE_FROM" != "$VL_SELF" ]; then
          return 0   # a NEW operator message → ANSWER wake
        fi
        # our own reply landed; ignore it and keep waiting for the next trigger.
      fi
      sleep "$step"; waited=$((waited + step))
    done
    WAKE_TEXT="__TICK__"; WAKE_FROM=""   # the tick elapsed with no new message → DEFEND tick
    return 0
  }

  echo "== violet up: self=$VL_SELF operator=$VL_OPERATOR DM=$VL_DM tick=${TICK}s =="
  # first turn: orient + one defend pass (VL_WAKE_TEXT empty ⇒ DEFEND per the role prompt).
  violet_turn "" ""
  local turn=1
  while [ "$turn" -lt "$MAX" ]; do
    wait_for_trigger
    turn=$((turn + 1))
    if [ "$WAKE_TEXT" = "__TICK__" ]; then
      echo "supervisor: tick $turn — defend pass"
    else
      echo "supervisor: turn $turn — woke on DM from ${WAKE_FROM:-?}"
    fi
    violet_turn "$WAKE_TEXT" "$WAKE_FROM"
  done
  echo "supervisor: hit VL_MAX_TURNS=$MAX — stopping"
}

# ---- live: run violet on a REAL bus (release-time only) -----------------------
# ADR-0039 "When it goes live": violet runs on the operator's LIVE bus ONLY at
# v0.5.0 release, after the operator's sign-off + tag, and after the `assistant`
# designation artifact is created. This path is the documented recipe; it is NOT
# run in CI. It assumes the active context's creds can register violet.
if [ "$MODE" = live ]; then
  : "${SEXTANT_STORE:?set SEXTANT_STORE to the live bus store}"
  SX="$(command -v sextant)"; SXMCP="$(command -v sextant-mcp)"
  [ -n "$SX" ] || { echo "sextant not on PATH"; exit 2; }
  command -v claude >/dev/null || { echo "claude not on PATH"; exit 2; }

  VL_WORK="${TMPDIR:-/tmp}/sextant-violet"; mkdir -p "$VL_WORK"
  echo "== register violet (uses your active context) =="
  "$SX" clients register violet --kind agent --store "$SEXTANT_STORE" --out "$VL_WORK/violet.creds" >/dev/null
  VL_SELF="$("$SX" clients list --store "$SEXTANT_STORE" --creds "$VL_WORK/violet.creds" | awk '/ violet /{print $1}' | head -1)"
  VL_OPERATOR="$("$SX" principal get --store "$SEXTANT_STORE" --creds "$VL_WORK/violet.creds" 2>/dev/null | grep -oE '01[0-9A-HJKMNP-TV-Z]{24}' | head -1)"
  [ -n "$VL_OPERATOR" ] || { echo "could not read principal"; exit 2; }
  if [ "$VL_OPERATOR" \< "$VL_SELF" ]; then DM="msg.topic.dm.$VL_OPERATOR.$VL_SELF"; else DM="msg.topic.dm.$VL_SELF.$VL_OPERATOR"; fi

  # The `assistant` designation artifact (ADR-0039) — created at RELEASE so the dash
  # + crew know violet is the live assistant. Created here as part of going live.
  rec="$(printf '{"$type":"document","client_id":"%s","name":"violet","accent":"#6a55e0"}' "$VL_SELF")"
  if "$SX" artifact get assistant --json --store "$SEXTANT_STORE" --creds "$VL_WORK/violet.creds" >/dev/null 2>&1; then
    rev="$("$SX" artifact get assistant --json --store "$SEXTANT_STORE" --creds "$VL_WORK/violet.creds" | (command -v jq >/dev/null && jq -r .Revision || grep -oE '"Revision":[0-9]+' | grep -oE '[0-9]+'))"
    "$SX" artifact update assistant "$rec" --rev "$rev" --store "$SEXTANT_STORE" --creds "$VL_WORK/violet.creds" >/dev/null
  else
    "$SX" artifact create assistant "$rec" --store "$SEXTANT_STORE" --creds "$VL_WORK/violet.creds" >/dev/null
  fi
  echo "== assistant designation set → violet ($VL_SELF) =="

  export SEXTANT_STORE VL_SEXTANT="$SX" VL_SEXTANT_MCP="$SXMCP"
  export VL_CREDS="$VL_WORK/violet.creds" VL_SELF VL_OPERATOR VL_DM="$DM"
  export VL_ROLE="$ROOT/docs/demos/violet-runtime.md" VL_WORK
  run_violet
  exit 0
fi

# ---- demo: hermetic, self-validating (default; CI-safe) -----------------------
# Stand up a throwaway bus, register a principal + violet, STUB `claude` so the
# supervisor loop is validated end-to-end (a DM wake → an answer turn; a tick →
# a defend turn that writes the curated `home` artifact) WITHOUT a live LLM or the
# operator's live bus. This proves the runtime MECHANISM.
if [ "$MODE" = demo ]; then
  command -v sextant >/dev/null || { echo "sextant not on PATH — run from a built tree"; exit 2; }
  SX="$(command -v sextant)"
  P="$(mktemp -d)"; trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$P"' EXIT
  S="$P/store"; mkdir -p "$S"
  pass=0; fail=0
  ok(){ echo "  PASS: $1"; pass=$((pass+1)); }
  no(){ echo "  FAIL: $1"; fail=$((fail+1)); }

  echo "== throwaway bus =="
  "$SX" up --store "$S" >/dev/null 2>&1 &
  for _ in $(seq 1 50); do [ -f "$S/bus.json" ] && break; sleep 0.1; done
  [ -f "$S/bus.json" ] || { echo "bus did not come up"; exit 2; }

  echo "== register operator (principal) + violet =="
  "$SX" clients register operator --kind human --store "$S" --out "$P/op.creds" >/dev/null
  "$SX" clients register violet --kind agent --store "$S" --out "$P/violet.creds" >/dev/null
  OP_ID="$("$SX" clients list --store "$S" --creds "$P/op.creds" | awk '/ operator /{print $1}' | head -1)"
  VL_ID="$("$SX" clients list --store "$S" --creds "$P/violet.creds" | awk '/ violet /{print $1}' | head -1)"
  "$SX" principal set "$OP_ID" --store "$S" --creds "$P/op.creds" >/dev/null 2>&1 || true
  if [ "$OP_ID" \< "$VL_ID" ]; then DM="msg.topic.dm.$OP_ID.$VL_ID"; else DM="msg.topic.dm.$VL_ID.$OP_ID"; fi

  # a `claude` STUB: instead of a live LLM, it performs the deterministic core of
  # whichever duty the wake selects, USING THE REAL sextant MCP path via the CLI —
  # so we validate the supervisor loop + the bus writes, not the model. It reads
  # VL_WAKE_TEXT/VL_WAKE_FROM exactly as the real role prompt would.
  STUB="$P/claude-stub.sh"
  cat >"$STUB" <<STUBEOF
#!/usr/bin/env bash
# stub claude: emulate violet's two duties deterministically over the bus CLI.
set -u
SX="$SX"; S="$S"; CREDS="$P/violet.creds"; DM="$DM"; OP="$OP_ID"
WT="\${VL_WAKE_TEXT:-}"; WF="\${VL_WAKE_FROM:-}"
if [ "\$WT" = "__TICK__" ] || [ -z "\$WT" ]; then
  # DEFEND: write the curated home projection (greeting + ranked pinned real calls).
  rec='{"\$type":"document","greeting":{"heading":"Good morning.","note":"1 real call needs you · 2 things handled themselves"},"blocks":[{"type":"pinned","names":["demo-brief"]}]}'
  if "\$SX" artifact get home --json --store "\$S" --creds "\$CREDS" >/dev/null 2>&1; then
    rev="\$("\$SX" artifact get home --json --store "\$S" --creds "\$CREDS" | grep -oE '"Revision":[0-9]+' | grep -oE '[0-9]+' | head -1)"
    "\$SX" artifact update home "\$rec" --rev "\$rev" --store "\$S" --creds "\$CREDS" >/dev/null
  else
    "\$SX" artifact create home "\$rec" --store "\$S" --creds "\$CREDS" >/dev/null
  fi
  echo "violet(defend): curated home"
elif [ "\$WF" = "\$OP" ]; then
  # ANSWER (read-only): reply on the operator DM.
  "\$SX" publish "\$DM" '{"\$type":"chat.message","text":"violet: the demo-brief is what needs you — it is at its gate."}' --store "\$S" --creds "\$CREDS" >/dev/null
  echo "violet(answer): replied on DM"
else
  echo "violet: situational awareness only (from \$WF); standing by"
fi
# emit a session id on the first (json) turn so --resume works on later turns
case " \$* " in *" --output-format json "*) echo '{"session_id":"stub-session","result":"ok"}';; esac
STUBEOF
  chmod +x "$STUB"

  # seed a candidate the defend pass should surface (a review-flagged brief).
  "$SX" artifact create demo-brief '{"$type":"document","title":"demo brief","body":"x","review":{"state":"review"}}' --store "$S" --creds "$P/violet.creds" >/dev/null

  export SEXTANT_STORE="$S" VL_SEXTANT="$SX" VL_SEXTANT_MCP="$(command -v sextant-mcp || echo /nonexistent)"
  export VL_CREDS="$P/violet.creds" VL_SELF="$VL_ID" VL_OPERATOR="$OP_ID" VL_DM="$DM"
  export VL_ROLE="$ROOT/docs/demos/violet-runtime.md" VL_WORK="$P"
  export VL_CLAUDE="$STUB" VL_TICK=2 VL_MAX_TURNS=8

  echo "== run the supervisor (first turn = defend; then a DM wake; then a tick) =="
  ( run_violet >"$P/run.log" 2>&1 ) &
  RUN_PID=$!
  # after the first defend turn settles, send a DM (operator asks a question) →
  # answer turn. Wait for violet's first curated home rather than a fixed sleep.
  for _ in $(seq 1 40); do
    "$SX" artifact get home --json --store "$S" --creds "$P/violet.creds" >/dev/null 2>&1 && break
    sleep 0.25
  done
  "$SX" publish "$DM" '{"$type":"chat.message","text":"where does the demo-brief stand?"}' --store "$S" --creds "$P/op.creds" >/dev/null
  # Deterministic: poll the run.log until BOTH a DM-wake turn AND a periodic tick
  # turn have run (bounded), then stop the loop — no wall-clock race.
  for _ in $(seq 1 80); do
    grep -q "woke on DM" "$P/run.log" 2>/dev/null && grep -q "tick .* defend pass" "$P/run.log" 2>/dev/null && break
    sleep 0.25
  done
  kill "$RUN_PID" 2>/dev/null || true; wait "$RUN_PID" 2>/dev/null || true

  echo "== validate =="
  "$SX" artifact get home --json --store "$S" --creds "$P/violet.creds" 2>/dev/null | grep -q 'real call needs you' \
    && ok "DEFEND: violet wrote the curated home projection (greeting + pinned)" \
    || no "DEFEND: home artifact not curated"
  "$SX" artifact get home --json --store "$S" --creds "$P/violet.creds" 2>/dev/null | grep -q '"pinned"' \
    && ok "DEFEND: curated home carries a ranked pinned (real-calls) block" \
    || no "DEFEND: no pinned block"
  "$SX" read "$DM" --since 0 --store "$S" --creds "$P/op.creds" 2>/dev/null | grep -q "violet: the demo-brief" \
    && ok "ANSWER: violet replied on the operator DM (read-only)" \
    || no "ANSWER: no reply on the DM"
  grep -q "tick .* defend pass" "$P/run.log" 2>/dev/null \
    && ok "TICK: the periodic defend tick fired (supervisor loop)" \
    || no "TICK: defend tick did not fire"
  grep -q "woke on DM" "$P/run.log" 2>/dev/null \
    && ok "WAKE: bus activity (a DM) woke violet for an answer turn" \
    || no "WAKE: DM did not wake violet"

  echo "== $pass passed, $fail failed =="
  [ "$fail" -eq 0 ] || { echo "see $P/run.log + $P/poc.log"; exit 1; }
  echo "violet runtime mechanism validated (hermetic; no live LLM, no live bus)."
  exit 0
fi

echo "usage: violet-runtime.sh (demo | live)"; exit 2
