#!/usr/bin/env bash
# violet-runtime-warm.sh — run violet, the operator's assistant, as a PSEUDO-AGENT
# on the sextant bus (ADR-0039 + the `violet-architecture` design): ONE permanent
# bus client (one identity) that internally fronts MULTIPLE models behind a
# wrapper. violet is a CLIENT LIKE ANY OTHER, driven by the role prompt
# (violet-runtime.md) + the violet-curation skill, talking to the bus through the
# sextant MCP under her OWN creds. Everything below is internal to the wrapper and
# invisible on the bus — a single `violet` client + the `assistant` designation.
#
# WHY THIS SHAPE (supersedes the spawn-per-turn violet-runtime.sh). The old
# runtime cold-started a fresh `claude -p` every turn (tens of seconds), AND it
# left the reply to the model to publish — which it sometimes forgot, so the
# operator saw nothing. Lena's pre-release test caught both. The fix is a wrapper:
#   1. CONTEXT-WARM conversation — a home-manager loop runs continuously, and as
#      it curates it refreshes a long-lived CONVERSATIONAL session's context with
#      the current workspace state (goals/briefs/status/review queue). So when an
#      operator DM arrives, the conversational model answers IMMEDIATELY from warm
#      context — NO agentic pre-read between her question and the answer. Warm
#      PROCESS alone isn't enough; the pre-loaded CONTEXT is what makes it instant.
#   2. RELIABLE OUTPUT CAPTURE — the wrapper does NOT depend on the model calling
#      message_publish (it forgets — the exact live bug). It reads the
#      conversational session's stdout stream and publishes the captured final
#      reply to the operator DM itself. The conversational session needs NO publish
#      tool for answers; losing a reply to stdout is structurally impossible.
#   3. MODEL SPLIT behind one client — a fast model (haiku) fronts conversation;
#      a capable model (sonnet) runs the home-manager curation. Both use violet's
#      single creds / one bus identity.
#
# VERIFIED EMPIRICALLY against the real `claude` binary (see the task report):
#   - `claude -p --input-format stream-json --output-format stream-json` accepts
#     MULTIPLE user messages injected over time into ONE long-lived process, each
#     a warm turn carrying full context (same session_id; turn 2+ ~1s vs ~4s cold;
#     prompt-cache hot).
#   - A pre-loaded `[context refresh]` message lets the next `[operator DM]` turn
#     answer straight from context in ~2.6s with num_turns=1 (no tool call / read).
#   - The per-turn `{"type":"result",...}` event carries the final assistant text
#     in `.result` — the wrapper publishes THAT, so capture never depends on the
#     model. Mid-session model switching cold-restarts + drops the cache, so the
#     split is TWO warm sessions behind one client, not one session swapping models.
#
#   demo  — self-validating run on a THROWAWAY hermetic bus (default; CI-safe).
#           Stubs `claude` with a stream-json reader/writer emitting the REAL
#           event shape, so the wrapper (context-warm loop + direct output capture
#           + model split) is validated WITHOUT a live LLM or live bus, and a
#           format mismatch cannot hide.
#   live  — run violet against a real store-based bus. RELEASE-TIME ONLY
#           (ADR-0039 "When it goes live"): operator sign-off + the `assistant`
#           designation artifact. Documented here; not exercised in CI.
#
# Usage:
#   docs/demos/violet-runtime-warm.sh demo
#   SEXTANT_STORE=<live-store> docs/demos/violet-runtime-warm.sh live   # release-time
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
MODE="${1:-demo}"

# ---- the wrapper: home-manager (curation+context) + conversational (answers) ---
# ONE bus identity, two warm sessions inside. Shared by demo + live. Expects set:
#   SEXTANT_STORE, VL_SEXTANT (sextant CLI), VL_SEXTANT_MCP (sextant-mcp),
#   VL_CREDS (violet's creds), VL_SELF (violet id), VL_OPERATOR (principal id),
#   VL_DM (operator DM subject), VL_ROLE (role-prompt path), VL_WORK (scratch dir).
# Optional: VL_TICK (seconds between defend ticks; default 900 = 15m),
#   VL_MAX_TURNS (safety cap), VL_CLAUDE (the `claude` binary; stubbed in demo),
#   VL_CONV_MODEL (conversation model; default haiku — fast),
#   VL_CURATE_MODEL (home-manager model; default sonnet — capable),
#   VL_TURN_TIMEOUT (seconds to await a turn's result before giving up).
run_violet() {
  : "${SEXTANT_STORE:?}"; : "${VL_CREDS:?}"; : "${VL_SELF:?}"; : "${VL_OPERATOR:?}"
  : "${VL_DM:?}"; : "${VL_ROLE:?}"; : "${VL_WORK:?}"
  local TICK="${VL_TICK:-900}" MAX="${VL_MAX_TURNS:-1000}" CLAUDE="${VL_CLAUDE:-claude}"
  local SX="${VL_SEXTANT:-sextant}" TURN_TIMEOUT="${VL_TURN_TIMEOUT:-180}"
  local CONV_MODEL="${VL_CONV_MODEL:-claude-haiku-4-5}"
  local CURATE_MODEL="${VL_CURATE_MODEL:-claude-sonnet-4-6}"
  # the GATE is the cheap, always-on triage: a haiku session that CLASSIFIES each
  # bus event as SIGNIFICANT (WAKE the deep agent) or not (SKIP, do nothing) —
  # Lena's cost model: haiku gates, sonnet only does deep work when woken. The
  # subjects it watches (NATS multi-wildcard catches crew + goals + artifact.<name>
  # discussion/change events, and the DM).
  local GATE_MODEL="${VL_GATE_MODEL:-claude-haiku-4-5}"
  local WATCH="${VL_WATCH:-msg.topic.>}"
  local MCP="$VL_WORK/violet.mcp.json"
  # workspace snapshot the home-manager writes + the wrapper injects. Exported as
  # VL_CONTEXT so a live home-manager session can read the path from its env (the
  # tick message also names it). The stub uses the same default path.
  local CTX="${VL_CONTEXT:-$VL_WORK/violet.context.txt}"
  export VL_CONTEXT="$CTX"

  # The violet-curation skill must be discoverable by `claude` so the home-manager
  # can load it. The plugin ships it under clients/claude-code/skills/; --add-dir
  # the plugin skills dir (and the repo root, so violet can Read design artifacts).
  local SKILLS_DIR="$ROOT/clients/claude-code/skills"

  # MCP config: violet talks to the bus through the sextant MCP under her OWN
  # creds. Loaded ONCE per session at start and kept warm. ONE identity for both.
  printf '{"mcpServers":{"sextant":{"command":"%s","env":{"SEXTANT_CREDS":"%s","SEXTANT_STORE":"%s"}}}}' \
    "${VL_SEXTANT_MCP:?}" "$VL_CREDS" "$SEXTANT_STORE" > "$MCP"

  # The home-manager (curation) session owns artifact writes (the `home`
  # projection — violet's owned work) + reads the workspace; it keeps the tools it
  # needs. The CONVERSATIONAL session is OUTPUT-CAPTURED: it gets Read only and NO
  # publish/artifact tools — the wrapper publishes its reply, so a forgotten
  # publish is impossible. Both are bounded by the role prompt's signal-not-manage.
  local CURATE_TOOLS="Read,mcp__sextant__message_read,mcp__sextant__message_subscribe,mcp__sextant__artifact_get,mcp__sextant__artifact_list,mcp__sextant__artifact_create,mcp__sextant__artifact_update,mcp__sextant__clients_list"
  local CONV_TOOLS="Read"
  # the gate is OUTPUT-CAPTURED like the conversational session: it classifies an
  # event (WAKE/SKIP) and emits that one word — Read only, no bus writes.
  local GATE_TOOLS="Read"

  # ---- launch a warm session: returns via globals <PFX>_PID, holds stdin on a fd.
  # Each session is one long-lived claude held open across all its turns: stdin is
  # a FIFO kept open on a dedicated fd (so claude never sees EOF until shutdown);
  # stdout streams to a per-session .jsonl the wrapper tails for `result` events.
  CONV_FIFO="$VL_WORK/conv.stdin"; CONV_OUT="$VL_WORK/conv.stdout.jsonl"
  CURATE_FIFO="$VL_WORK/curate.stdin"; CURATE_OUT="$VL_WORK/curate.stdout.jsonl"
  GATE_FIFO="$VL_WORK/gate.stdin"; GATE_OUT="$VL_WORK/gate.stdout.jsonl"

  start_session() {  # $1=fifo $2=out $3=model $4=tools $5=fd $6=pidvar
    local fifo="$1" out="$2" model="$3" tools="$4" fd="$5"
    rm -f "$fifo"; mkfifo "$fifo"; : > "$out"
    "$CLAUDE" -p --input-format stream-json --output-format stream-json --verbose \
      --append-system-prompt-file "$VL_ROLE" --mcp-config "$MCP" --strict-mcp-config \
      --add-dir "$SKILLS_DIR" --add-dir "$ROOT" --permission-mode acceptEdits \
      --allowedTools "$tools" --model "$model" < "$fifo" > "$out" 2>>"$VL_WORK/violet.stderr" &
    eval "$6=\$!"           # store pid in the named var
    eval "exec $fd>\"$fifo\""  # hold the write end open on the chosen fd
  }

  # Three warm sessions, ONE violet client (one bus identity): a haiku GATE
  # (cheap significance triage on every event) wakes the sonnet HOME-MANAGER (deep
  # context refresh + Home curation) only on significant events; the haiku
  # CONVERSATIONAL session answers operator DMs from the context sonnet keeps fresh.
  start_session "$CONV_FIFO"   "$CONV_OUT"   "$CONV_MODEL"   "$CONV_TOOLS"   8 CONV_PID
  start_session "$CURATE_FIFO" "$CURATE_OUT" "$CURATE_MODEL" "$CURATE_TOOLS" 7 CURATE_PID
  start_session "$GATE_FIFO"   "$GATE_OUT"   "$GATE_MODEL"   "$GATE_TOOLS"   6 GATE_PID

  cleanup_warm() {
    exec 8>&- 2>/dev/null || true
    exec 7>&- 2>/dev/null || true
    exec 6>&- 2>/dev/null || true
    kill "$CONV_PID" "$CURATE_PID" "$GATE_PID" 2>/dev/null || true
    wait "$CONV_PID" 2>/dev/null || true; wait "$CURATE_PID" 2>/dev/null || true
    wait "$GATE_PID" 2>/dev/null || true
    rm -f "$CONV_FIFO" "$CURATE_FIFO" "$GATE_FIFO"
  }
  trap cleanup_warm RETURN

  result_count() { grep -c '"type":"result"' "$1" 2>/dev/null || true; }

  # inject_and_wait FD OUTFILE PID TEXT — write one stream-json user message to a
  # session's stdin (FD), then BLOCK until it emits the next `result` line (turn
  # done) or the deadline elapses. This is the warm turn: no cold start, session
  # reused. Echoes nothing; callers read the captured text from OUTFILE.
  inject_and_wait() {
    local fd="$1" out="$2" pid="$3" text="$4" before after waited=0
    before="$(result_count "$out")"
    if command -v jq >/dev/null; then
      jq -cn --arg t "$text" '{type:"user",message:{role:"user",content:[{type:"text",text:$t}]}}' >&"$fd"
    else
      local esc=${text//\\/\\\\}; esc=${esc//\"/\\\"}; esc=${esc//$'\n'/\\n}
      printf '{"type":"user","message":{"role":"user","content":[{"type":"text","text":"%s"}]}}\n' "$esc" >&"$fd"
    fi
    while [ "$waited" -lt "$TURN_TIMEOUT" ]; do
      after="$(result_count "$out")"
      [ "${after:-0}" -gt "${before:-0}" ] && return 0
      if ! kill -0 "$pid" 2>/dev/null; then
        echo "supervisor: a WARM SESSION DIED mid-turn — see $VL_WORK/violet.stderr" >&2
        return 1
      fi
      sleep 1; waited=$((waited + 1))
    done
    echo "supervisor: turn exceeded VL_TURN_TIMEOUT=${TURN_TIMEOUT}s (still warm; continuing)" >&2
    return 0
  }

  # capture_last_reply OUTFILE — extract the final assistant text of the most
  # recent turn from a session's stdout (the `result` event's `.result`). THIS is
  # what the wrapper publishes — reliable output capture, never the model's job.
  capture_last_reply() {
    local out="$1"
    if command -v jq >/dev/null; then
      grep '"type":"result"' "$out" 2>/dev/null | tail -1 | jq -r '.result // empty' 2>/dev/null
    else
      grep '"type":"result"' "$out" 2>/dev/null | tail -1 | sed -E 's/.*"result":"(.*)"\}$/\1/'
    fi
  }

  # publish_reply TEXT — the wrapper publishes the captured conversational reply to
  # the operator DM under violet's creds. (Output capture: the conversational
  # session never needs the publish tool; this is the wrapper's job, not the
  # model's. The bus author-stamps it violet, so the cursor ignores it as own.)
  publish_reply() {
    local text="$1"
    [ -z "$text" ] && return 0
    local rec
    if command -v jq >/dev/null; then
      rec="$(jq -cn --arg t "$text" '{"$type":"chat.message",text:$t}')"
    else
      local esc=${text//\\/\\\\}; esc=${esc//\"/\\\"}; esc=${esc//$'\n'/ }
      rec="$(printf '{"$type":"chat.message","text":"%s"}' "$esc")"
    fi
    "$SX" publish "$VL_DM" "$rec" --store "$SEXTANT_STORE" --creds "$VL_CREDS" >/dev/null 2>&1 || true
  }

  # refresh_context — inject the CURRENT workspace snapshot into the conversational
  # session so the next operator DM is answered from context with NO pre-read. The
  # snapshot is in $CTX, kept current by the maintainer (per-event) and the
  # home-manager (periodic) — both via output-capture, never a session file write.
  refresh_context() {
    [ -s "$CTX" ] || return 0
    inject_and_wait 8 "$CONV_OUT" "$CONV_PID" "[context refresh] Current workspace state (answer the operator from THIS only; if something isn't here, say you'll need to check rather than guess):
$(cat "$CTX")"
  }

  # gate_classify EVENT_TEXT — the cheap, always-on triage (haiku). Classify one
  # new bus event as SIGNIFICANT (wake the deep agent) or not (skip). The gate is
  # warm + output-captured: it emits exactly WAKE or SKIP and the wrapper branches
  # on it. Self-authored events are filtered out before this is ever called (no
  # self-trigger loop). The significance rule is in the role prompt / curation
  # skill (start: artifact review-ready, approval/verdict, goal/criterion change,
  # operator DM; ignore WIP / status churn / chatter). Returns 0 = WAKE, 1 = SKIP.
  gate_classify() {
    local event="$1" verdict
    inject_and_wait 6 "$GATE_OUT" "$GATE_PID" "[bus event] Classify whether this event is SIGNIFICANT enough to wake the deep curation agent, per your significance rule (significant: an artifact became review-ready, an approval/verdict, a goal/criterion state change, an operator DM; NOT significant: work-in-progress updates, routine chatter, agent.status churn). Reply with EXACTLY one word — WAKE or SKIP — nothing else.

EVENT:
$event" || return 1
    verdict="$(capture_last_reply "$GATE_OUT" | tr '[:lower:]' '[:upper:]')"
    case "$verdict" in
      *WAKE*) return 0 ;;
      *) return 1 ;;
    esac
  }

  # dm_count — how many messages are on violet's DM right now (a COUNT cursor; the
  # DM path only needs "is there a new non-self message", not per-frame replay).
  dm_count() { "$SX" read "$VL_DM" --since 0 --json --store "$SEXTANT_STORE" --creds "$VL_CREDS" 2>/dev/null | grep -c '"kind": "message"' || true; }

  # next_cursor SUBJECT — the bus's reported "next cursor" for a subject (the
  # position AFTER the last frame). We seed EV_CUR with this at startup so we never
  # replay history, then read --since EV_CUR to get exactly the NEW frames.
  next_cursor() {
    "$SX" read "$1" --since 0 --store "$SEXTANT_STORE" --creds "$VL_CREDS" 2>/dev/null \
      | sed -nE 's/.*next cursor ([0-9]+).*/\1/p' | tail -1
  }

  # pop_event — read the SINGLE next new frame on $WATCH at cursor EV_CUR, advance
  # EV_CUR by one, and (if it's not self-authored) set EV_AUTHOR/WAKE_EVENT to
  # "<author> said (on <subj>): <text>". Returns 0 if a non-self event is ready,
  # 1 if there was nothing new or it was self-authored (so we never re-trigger on
  # our own published frames). Cursor-based ⇒ each frame is processed exactly once,
  # in order — no newest-frame race when several land between polls.
  EV_AUTHOR=""
  pop_event() {
    local line author text subj
    # one frame at EV_CUR (plain read; drop the trailing summary line).
    line="$("$SX" read "$WATCH" --since "$EV_CUR" --limit 1 --store "$SEXTANT_STORE" --creds "$VL_CREDS" 2>/dev/null | grep -v 'messages; next cursor' | head -1 || true)"
    [ -z "$line" ] && return 1                       # nothing new at this cursor
    EV_CUR=$((EV_CUR + 1))                            # consume this frame
    # frame shape: [subject] <id> <author> {record-json}
    subj="$(printf '%s' "$line" | sed -E 's/^\[([^]]*)\].*/\1/')"
    author="$(printf '%s' "$line" | sed -E 's/^\[[^]]*\] +[^ ]+ +<([^>]*)>.*/\1/')"
    text="$(printf '%s' "$line" | sed -E 's/.*"text": *"(.*)"\}.*/\1/')"
    EV_AUTHOR="$author"
    [ "$author" = "$VL_SELF" ] && return 1            # our own frame — ignore (no self-trigger)
    WAKE_EVENT="$(printf '%s said (on %s): %s' "${author:-someone}" "${subj:-bus}" "$text")"
    return 0
  }

  # wait_for_trigger blocks until ONE of: a NEW operator DM (ANSWER), a NEW bus
  # event on $WATCH (GATE), or the tick (safety-net DEFEND). The DM path uses a
  # COUNT cursor; the event path uses a real per-frame cursor (EV_CUR) so each
  # event is triaged exactly once, in order. Self-authored frames are filtered so a
  # published reply never re-triggers. The gate is the FAST path (no tick wait).
  WAKE_TEXT=""; WAKE_FROM=""; WAKE_EVENT=""; DM_SEEN="$(dm_count)"
  EV_CUR="$(next_cursor "$WATCH")"; EV_CUR="${EV_CUR:-1}"
  wait_for_trigger() {
    local waited=0 step=1 nowdm frame
    while [ "$waited" -lt "$TICK" ]; do
      # 1) a NEW operator DM → ANSWER (highest priority — she's waiting).
      nowdm="$(dm_count)"
      if [ "${nowdm:-0}" -gt "${DM_SEEN:-0}" ]; then
        DM_SEEN="$nowdm"
        frame="$("$SX" read "$VL_DM" --since 0 --json --store "$SEXTANT_STORE" --creds "$VL_CREDS" 2>/dev/null || true)"
        WAKE_FROM="$(printf '%s' "$frame" | grep '"author"' | tail -1 | sed -E 's/.*"author": *"([^"]*)".*/\1/' || true)"
        WAKE_TEXT="$(printf '%s' "$frame" | grep '"text"'   | tail -1 | sed -E 's/.*"text": *"(.*)".*/\1/'   || true)"
        if [ "$WAKE_FROM" != "$VL_SELF" ]; then return 0; fi   # NEW operator DM → ANSWER
        # our own reply landed; ignore it (the answer path advances EV_CUR for us).
      fi
      # 2) the NEXT new bus event on the watched subjects → GATE.
      if pop_event; then
        WAKE_TEXT="__EVENT__"; WAKE_FROM="$EV_AUTHOR"
        return 0
      fi
      sleep "$step"; waited=$((waited + step))
    done
    WAKE_TEXT="__TICK__"; WAKE_FROM=""   # tick elapsed → safety-net deep curation
    return 0
  }

  echo "== violet WARM (pseudo-agent) up: self=$VL_SELF operator=$VL_OPERATOR DM=$VL_DM safety-tick=${TICK}s watch=$WATCH =="
  echo "   conversational=$CONV_MODEL (pid $CONV_PID)  home-manager=$CURATE_MODEL (pid $CURATE_PID, woken by gate)  gate=$GATE_MODEL (pid $GATE_PID, triages every event)"

  # home_manager_pass — the continuous loop's work, in two halves:
  #   (1) the sonnet home-manager session re-curates the `home` artifact (its
  #       owned-work artifact MCP tools) AND ends its turn by EMITTING a compact
  #       current-workspace snapshot as its reply text;
  #   (2) the WRAPPER captures that emitted snapshot from the session's output
  #       stream (same reliable path as the conversational reply) and writes it to
  #       $CTX, then injects it into the conversational session as a
  #       `[context refresh]`.
  # The session itself never writes $CTX — it has no Write tool. Snapshot delivery
  # is output-capture, exactly like the answer reply. The snapshot must reflect
  # LIVE state, so the home-manager READS the live goal + review queue + gated
  # briefs via its artifact tools before emitting.
  home_manager_pass() {
    inject_and_wait 7 "$CURATE_OUT" "$CURATE_PID" \
      "[defend tick] Re-curate Home now per the violet-curation skill (write the home artifact via your artifact tools). Then READ the LIVE workspace with your artifact tools — \`goal.v0-5-0\` (each criterion + its current status), the current review queue (artifacts with review.state=review), and any briefs at their gate — and END this turn by REPLYING with a COMPACT, CURRENT snapshot of that state (a few short lines: where v0.5.0 stands criterion-by-criterion, what's at its gate, who's doing what). Reply with the snapshot text ONLY — no preamble. The wrapper captures your reply and feeds it to the conversational side; do not try to write a file." || true
    # capture the emitted snapshot and persist it for the conversational refresh.
    local snap; snap="$(capture_last_reply "$CURATE_OUT")"
    [ -n "$snap" ] && printf '%s\n' "$snap" > "$CTX"
    refresh_context || true
  }

  # turn 1: orient + first home-manager pass (deep curate + seed the warm context).
  home_manager_pass
  echo "supervisor: turn 1 — startup home-manager pass (all sessions warm)"
  local turn=1
  while [ "$turn" -lt "$MAX" ]; do
    wait_for_trigger
    turn=$((turn + 1))
    case "$WAKE_TEXT" in
      __TICK__)
        # SAFETY-NET tick: the gate is the primary trigger; this slow fallback just
        # re-curates + resyncs in case something was missed (a long quiet period, a
        # dropped event). Keep $VL_TICK long in production.
        echo "supervisor: safety-tick $turn — home-manager pass (deep curate + context resync)"
        home_manager_pass
        ;;
      __EVENT__)
        # EVENT-DRIVEN: a new bus event landed. The cheap haiku GATE triages it;
        # only a SIGNIFICANT event wakes the sonnet deep refresh + curation (cost
        # control — most events SKIP and cost only the gate classification).
        if gate_classify "$WAKE_EVENT"; then
          echo "supervisor: event $turn — SIGNIFICANT (${WAKE_FROM:-?}); waking deep refresh (sonnet)"
          home_manager_pass
        else
          echo "supervisor: event $turn — not significant (${WAKE_FROM:-?}); skipped (gate only, no deep work)"
        fi
        ;;
      *)
        # ANSWER: a new operator DM (always significant). Wake the deep refresh so
        # the context reflects the just-arrived message, then the conversational
        # session replies from warm context; the WRAPPER captures + publishes.
        echo "supervisor: turn $turn — woke on DM from ${WAKE_FROM:-?} (refresh + answer)"
        home_manager_pass
        inject_and_wait 8 "$CONV_OUT" "$CONV_PID" "[operator DM] $WAKE_TEXT" || true
        reply="$(capture_last_reply "$CONV_OUT")"
        publish_reply "$reply"
        echo "supervisor: turn $turn — captured + published reply (${#reply} chars)"
        # the DM (and our just-published reply) are under $WATCH; advance the event
        # cursor past them so we don't re-triage them as generic bus events.
        EV_CUR="$(next_cursor "$WATCH")"; EV_CUR="${EV_CUR:-$EV_CUR}"
        ;;
    esac
  done
  echo "supervisor: hit VL_MAX_TURNS=$MAX — stopping"
}

# ---- live: run violet on a REAL bus (release-time only) -----------------------
# ADR-0039 "When it goes live": violet runs on the operator's LIVE bus ONLY at
# v0.5.0 release, after the operator's sign-off + tag, and after the `assistant`
# designation artifact is created. Documented recipe; NOT run in CI.
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
  # + crew know violet is the live assistant.
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
# Stand up a throwaway bus, register a principal + violet, STUB `claude` with a
# STREAM-JSON reader/writer (emits the REAL event shape). The stub stands in for
# ALL THREE sessions (conversational, home-manager, gate): each is ONE long-lived
# process; one turn per stdin line, dispatched by the turn's prefix. This proves
# the WRAPPER mechanism — the GATE triaging events (WIP→SKIP, review-ready→WAKE),
# event-driven deep refresh with no tick, context-warm answers, DIRECT output
# capture (the wrapper publishes; the model does NOT) — without a live LLM or bus.
if [ "$MODE" = demo ]; then
  command -v sextant >/dev/null || { echo "sextant not on PATH — run from a built tree"; exit 2; }
  SX="$(command -v sextant)"
  P="$(mktemp -d)"; KEEP=0
  # keep the scratch dir on failure (so the printed debug paths exist); wipe on ok.
  trap 'kill $(jobs -p) 2>/dev/null; [ "${KEEP:-0}" = 1 ] || rm -rf "$P"' EXIT
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

  # a STREAM-JSON `claude` STUB serving BOTH sessions. It stays ALIVE for the whole
  # session (one process, N turns): READS injected user-message JSON lines from
  # stdin (the same envelope the wrapper injects) and, per message, performs the
  # deterministic core of whichever duty the prefix selects, using the REAL sextant
  # CLI. It emits the REAL stream-json event shape (system/init, assistant, result)
  # on stdout — so a format mismatch can't hide AND the wrapper's output capture
  # (reading `.result`) is exercised against the true shape. Critically, on an
  # ANSWER turn the conversational stub does NOT publish — it just answers from the
  # injected context — proving the WRAPPER captures + publishes, not the model.
  STUB="$P/claude-stub.sh"
  cat >"$STUB" <<STUBEOF
#!/usr/bin/env bash
# stub claude (stream-json): one long-lived process; one turn per stdin line.
set -u
SX="$SX"; S="$S"; CREDS="$P/violet.creds"; DM="$DM"; OP="$OP_ID"; VL="$VL_ID"
CTX="$P/violet.context.txt"; SID="stub-warm-\$\$"
LAST_CTX=""   # most recent injected workspace snapshot (the warm context)

emit_init() { printf '{"type":"system","subtype":"init","session_id":"%s","model":"stub","tools":[]}\n' "\$SID"; }
emit_assistant() { printf '{"type":"assistant","session_id":"%s","message":{"role":"assistant","content":[{"type":"text","text":"%s"}]}}\n' "\$SID" "\$1"; }
emit_result() {
  # \$1 = final text. The wrapper reads THIS .result to publish — never a stub publish.
  if command -v jq >/dev/null; then
    jq -cn --arg s "\$SID" --arg r "\$1" '{type:"result",subtype:"success",is_error:false,session_id:\$s,num_turns:1,result:\$r}'
  else
    printf '{"type":"result","subtype":"success","is_error":false,"session_id":"%s","num_turns":1,"result":"%s"}\n' "\$SID" "\$1"
  fi
}

emit_init   # announce the session once (real claude emits init at startup too).

while IFS= read -r line; do
  [ -z "\$line" ] && continue
  if command -v jq >/dev/null; then
    text="\$(printf '%s' "\$line" | jq -r '.message.content[0].text // empty' 2>/dev/null)"
  else
    text="\$(printf '%s' "\$line" | sed -E 's/.*"text":"(.*)"\}\].*/\1/')"
  fi
  case "\$text" in
    "[defend tick]"*)
      # HOME-MANAGER (sonnet session): curate the home artifact via its artifact
      # MCP tools (here, the CLI), then READ LIVE state — ENUMERATE every
      # review-flagged artifact right now (so a newly-created one shows up
      # immediately) — and EMIT a compact snapshot as the turn's RESULT TEXT. It
      # does NOT write \$CTX (no Write tool); the WRAPPER captures this .result.
      # build the live list of review-ready artifacts (names), excluding home.
      ready=""
      for a in \$("\$SX" artifact list --store "\$S" --creds "\$CREDS" 2>/dev/null | grep -v 'artifacts)' | awk '{print \$1}'); do
        [ "\$a" = "home" ] && continue
        if "\$SX" artifact get "\$a" --json --store "\$S" --creds "\$CREDS" 2>/dev/null | grep -qE '"state": *"review"'; then
          ready="\${ready:+\$ready, }[[\$a]]"
        fi
      done
      [ -n "\$ready" ] || ready="(none)"
      rec="\$(printf '{"\$type":"document","greeting":{"heading":"Good morning.","note":"real calls need you"},"blocks":[{"type":"pinned","names":["demo-brief"]}]}')"
      if "\$SX" artifact get home --json --store "\$S" --creds "\$CREDS" >/dev/null 2>&1; then
        rev="\$("\$SX" artifact get home --json --store "\$S" --creds "\$CREDS" | grep -oE '"Revision":[0-9]+' | grep -oE '[0-9]+' | head -1)"
        "\$SX" artifact update home "\$rec" --rev "\$rev" --store "\$S" --creds "\$CREDS" >/dev/null
      else
        "\$SX" artifact create home "\$rec" --store "\$S" --creds "\$CREDS" >/dev/null
      fi
      # EMIT the snapshot as the result text (NO file write). It lists the LIVE
      # review-ready set — so a just-created artifact appears after a WAKE pass.
      emit_assistant "snapshot emitted"
      emit_result "v0.5.0: in progress. At their gate, waiting on you: \$ready."
      ;;
    "[context refresh]"*)
      # CONVERSATIONAL session: absorb the warm workspace context. No bus action.
      # Stash it so the next answer comes FROM this snapshot (context-warm).
      LAST_CTX="\$text"
      emit_assistant "ok"
      emit_result "context absorbed"
      ;;
    "[bus event]"*)
      # GATE (haiku session): classify the event as WAKE or SKIP per the
      # significance rule. Emit EXACTLY one word as the result — the wrapper
      # branches on it. SIGNIFICANT: review-ready / approval / verdict / gate /
      # goal-criterion change / operator. NOT: WIP / "working on" / status churn.
      # Classify ONLY the EVENT portion (the line(s) after the "EVENT:" marker) —
      # the prompt itself states the significance rule, which would otherwise match
      # the heuristic regex. (The marker is on its own line; the event follows.)
      evtxt="\$(printf '%s' "\$text" | sed -n '/EVENT:/,\$p' | sed '1d')"
      if printf '%s' "\$evtxt" | grep -qiE 'review-ready|ready for review|flagged .*review|approv|verdict|sign-off|at its gate|waiting on you|criterion|goal .*(advanced|complete)'; then
        verdict="WAKE"
      else
        verdict="SKIP"
      fi
      emit_assistant "\$verdict"
      emit_result "\$verdict"
      ;;
    "[operator DM]"*)
      # CONVERSATIONAL session: answer FROM WARM CONTEXT (\$LAST_CTX) — NO pre-read,
      # NO publish. The wrapper captures this .result text and publishes it. Answer
      # respects the operator's bar: <=250 chars, plain text, [[wikilinks]] only.
      # The answer ECHOES the "at their gate" line from the injected snapshot, so
      # whatever the latest snapshot listed (incl. an event-driven addition) shows.
      gateln="\$(printf '%s' "\$LAST_CTX" | grep -i 'at their gate' | head -1 | sed -E 's/.*at their gate[^:]*: *//I')"
      if [ -n "\$gateln" ]; then
        ans="At their gate, waiting on you: \$gateln"
      else
        ans="I don't have that in my current context — I'll check and follow up."
      fi
      # keep within the 250-char bar.
      ans="\$(printf '%s' "\$ans" | cut -c1-250)"
      emit_assistant "\$ans"
      emit_result "\$ans"
      ;;
    *)
      emit_assistant "standing by"
      emit_result "standing by"
      ;;
  esac
done
STUBEOF
  chmod +x "$STUB"

  # seed a candidate the home-manager pass should surface (a review-flagged brief).
  "$SX" artifact create demo-brief '{"$type":"document","title":"demo brief","body":"x","review":{"state":"review"}}' --store "$S" --creds "$P/violet.creds" >/dev/null

  # crew topic violet watches (under $WATCH = msg.topic.>). Peers publish here.
  CREW="msg.topic.crew"

  export SEXTANT_STORE="$S" VL_SEXTANT="$SX" VL_SEXTANT_MCP="$(command -v sextant-mcp || echo /nonexistent)"
  export VL_CREDS="$P/violet.creds" VL_SELF="$VL_ID" VL_OPERATOR="$OP_ID" VL_DM="$DM"
  export VL_ROLE="$ROOT/docs/demos/violet-runtime.md" VL_WORK="$P"
  # safety-tick set HIGH so it does NOT fire during the test — proving the gate
  # (not the tick) is what keeps context current. VL_MAX_TURNS bounds the run.
  export VL_CLAUDE="$STUB" VL_TICK=3600 VL_MAX_TURNS=12 VL_TURN_TIMEOUT=30

  echo "== run the WRAPPER (gate triages events → wakes deep refresh only on significant ones) =="
  ( run_violet >"$P/run.log" 2>&1 ) &
  RUN_PID=$!
  # wait for the startup home-manager pass to seed the context.
  for _ in $(seq 1 80); do
    [ -s "$P/violet.context.txt" ] && break; sleep 0.25
  done

  # (1) a WIP-ish bus event → gate should SKIP (no deep pass). Snapshot a marker so
  # we can prove no home-manager turn fired in response.
  ht_before_wip="$(grep -c '"type":"result"' "$P/curate.stdout.jsonl" 2>/dev/null || echo 0)"
  "$SX" publish "$CREW" '{"$type":"chat.message","text":"canopus: still working on the flaky e2e test, about halfway"}' --store "$S" --creds "$P/op.creds" >/dev/null
  for _ in $(seq 1 40); do grep -q "not significant" "$P/run.log" 2>/dev/null && break; sleep 0.25; done
  ht_after_wip="$(grep -c '"type":"result"' "$P/curate.stdout.jsonl" 2>/dev/null || echo 0)"

  # (2) a SIGNIFICANT event → gate WAKEs the deep refresh IMMEDIATELY (no tick).
  # First create the new review-ready artifact, THEN publish the event about it.
  "$SX" artifact create leaf-runbook '{"$type":"document","title":"leaf setup runbook","review":{"state":"review"}}' --store "$S" --creds "$P/violet.creds" >/dev/null
  "$SX" publish "$CREW" '{"$type":"chat.message","text":"vega flagged leaf-runbook ready for review"}' --store "$S" --creds "$P/op.creds" >/dev/null
  # wait until the context reflects the new artifact (the event-driven refresh).
  for _ in $(seq 1 80); do grep -q 'leaf-runbook' "$P/violet.context.txt" 2>/dev/null && break; sleep 0.25; done

  # (3) operator DM → answer should reflect leaf-runbook (from the just-refreshed
  # warm context), with NO safety-tick having fired.
  "$SX" publish "$DM" '{"$type":"chat.message","text":"what needs me right now?"}' --store "$S" --creds "$P/op.creds" >/dev/null
  for _ in $(seq 1 80); do grep -q "captured + published reply" "$P/run.log" 2>/dev/null && break; sleep 0.25; done
  kill "$RUN_PID" 2>/dev/null || true; wait "$RUN_PID" 2>/dev/null || true

  echo "== validate =="
  "$SX" artifact get home --json --store "$S" --creds "$P/violet.creds" 2>/dev/null | grep -q '"pinned"' \
    && ok "DEFEND: home-manager curated the home projection (greeting + pinned)" \
    || no "DEFEND: home artifact not curated"
  # GATE — SKIP: the WIP event did NOT wake the deep agent (no extra home-manager
  # turn fired in response to it). This is the cost control: routine churn is cheap.
  grep -q "not significant" "$P/run.log" 2>/dev/null \
    && ok "GATE-SKIP: the WIP event was triaged not-significant (gate only, no deep pass)" \
    || no "GATE-SKIP: WIP event was not skipped by the gate"
  { [ "${ht_after_wip:-0}" -eq "${ht_before_wip:-0}" ]; } \
    && ok "GATE-SKIP: the home-manager did NOT run for the WIP event (no deep work; cost saved)" \
    || no "GATE-SKIP: home-manager ran on a WIP event (gate failed to skip): ${ht_before_wip} to ${ht_after_wip}"
  # GATE — WAKE: the significant (review-ready) event woke the deep refresh, and the
  # context now reflects the NEW artifact — within seconds, with NO tick having fired.
  grep -q "SIGNIFICANT" "$P/run.log" 2>/dev/null \
    && ok "GATE-WAKE: the review-ready event was triaged significant → woke the deep refresh" \
    || no "GATE-WAKE: significant event did not wake the deep refresh"
  grep -q 'leaf-runbook' "$P/violet.context.txt" 2>/dev/null \
    && ok "EVENT-DRIVEN: context reflects the new artifact within seconds (no tick needed)" \
    || no "EVENT-DRIVEN: context did NOT pick up the event-driven artifact"
  ! grep -q "supervisor: safety-tick" "$P/run.log" 2>/dev/null \
    && ok "EVENT-DRIVEN: the freshness came from the GATE, not the safety-tick (tick never fired)" \
    || no "EVENT-DRIVEN: a safety-tick fired — can't attribute freshness to the gate"
  # CONTEXT-WARM: $CTX is written by the WRAPPER from the home-manager's EMITTED
  # result (output-capture), not a session file write; conversational absorbs it.
  [ -s "$P/violet.context.txt" ] && grep -q 'leaf-runbook' "$P/violet.context.txt" 2>/dev/null \
    && ok "CONTEXT-WARM: wrapper captured the home-manager's emitted snapshot into \$CTX (not a file write)" \
    || no "CONTEXT-WARM: \$CTX not populated from the home-manager's output"
  grep -q "context absorbed" "$P/conv.stdout.jsonl" 2>/dev/null \
    && ok "CONTEXT-WARM: the conversational session absorbed a [context refresh] before answering" \
    || no "CONTEXT-WARM: conversational session never got a context refresh"
  # OUTPUT CAPTURE: the conversational stub did NOT publish; the WRAPPER did.
  reply_line="$("$SX" read "$DM" --since 0 --store "$S" --creds "$P/op.creds" 2>/dev/null | grep 'leaf-runbook' | tail -1 || true)"
  printf '%s' "$reply_line" | grep -q 'leaf-runbook' \
    && ok "OUTPUT-CAPTURE: wrapper captured the reply from the stream + published it to the DM" \
    || no "OUTPUT-CAPTURE: no reply on the DM (or it didn't reflect leaf-runbook)"
  grep -q "captured + published reply" "$P/run.log" 2>/dev/null \
    && ok "OUTPUT-CAPTURE: wrapper (not the model) performed the publish" \
    || no "OUTPUT-CAPTURE: wrapper did not report a captured publish"
  # ANSWER-FROM-CONTEXT: the reply reflects leaf-runbook — which only entered via the
  # event-driven refresh — proving it's from the warm context, not stale/training.
  printf '%s' "$reply_line" | grep -q 'leaf-runbook' \
    && ok "ANSWER-FROM-CONTEXT: the answer reflects the event-driven update (warm context, not stale)" \
    || no "ANSWER-FROM-CONTEXT: answer did not reflect the event-driven context"
  grep -q "woke on DM" "$P/run.log" 2>/dev/null \
    && ok "WAKE: an operator DM woke violet for an answer turn" \
    || no "WAKE: DM did not wake violet"
  # MODEL SPLIT: THREE distinct sessions ran behind the one violet client.
  { [ -s "$P/conv.stdout.jsonl" ] && [ -s "$P/curate.stdout.jsonl" ] && [ -s "$P/gate.stdout.jsonl" ]; } \
    && ok "MODEL-SPLIT: three warm sessions (conversation + home-manager + gate) behind ONE violet client" \
    || no "MODEL-SPLIT: expected three session streams"
  # WARM: each session is ONE process — exactly one init per stream, reused turns.
  ci="$(grep -c '"subtype":"init"' "$P/conv.stdout.jsonl" 2>/dev/null || echo 0)"
  ct="$(grep -c '"type":"result"' "$P/conv.stdout.jsonl" 2>/dev/null || echo 0)"
  hi="$(grep -c '"subtype":"init"' "$P/curate.stdout.jsonl" 2>/dev/null || echo 0)"
  ht="$(grep -c '"type":"result"' "$P/curate.stdout.jsonl" 2>/dev/null || echo 0)"
  gi="$(grep -c '"subtype":"init"' "$P/gate.stdout.jsonl" 2>/dev/null || echo 0)"
  gt="$(grep -c '"type":"result"' "$P/gate.stdout.jsonl" 2>/dev/null || echo 0)"
  echo "  (warm: conv $ci init/$ct turns · home-manager $hi init/$ht turns · gate $gi init/$gt turns — one process each)"
  { [ "${ci:-0}" -eq 1 ] && [ "${hi:-0}" -eq 1 ] && [ "${gi:-0}" -eq 1 ] && [ "${gt:-0}" -ge 2 ]; } \
    && ok "WARM: one process per session, one init each, gate reused across events — no cold start" \
    || no "WARM: expected 1 init/session + a reused gate (conv $ci/$ct · home $hi/$ht · gate $gi/$gt)"
  # OWN-REPLY: the wrapper-published reply must NOT re-trigger violet — exactly ONE
  # answer turn for the ONE operator DM (the gate filters self-authored events too).
  answers="$(grep -c "woke on DM" "$P/run.log" 2>/dev/null || echo 0)"
  { [ "${answers:-0}" -eq 1 ]; } \
    && ok "OWN-REPLY: violet's published reply did not re-trigger her (1 answer for 1 DM)" \
    || no "OWN-REPLY: expected exactly 1 answer turn, got $answers (self-reply loop?)"
  # BREVITY (operator's explicit bar): the published answer must be <=250 chars,
  # plain text, no markdown formatting (no **bold**, # headers, or - bullets);
  # [[wikilinks]] are allowed. Pull violet's published chat.message text from a
  # --json read (the record.text of her reply frame).
  if command -v jq >/dev/null; then
    ans_text="$("$SX" read "$DM" --since 0 --json --store "$S" --creds "$P/op.creds" 2>/dev/null \
      | jq -rs '[.[] | select(type=="object" and .author=="'"$VL_ID"'" and .record.text) | .record.text] | last // empty' 2>/dev/null)"
  fi
  # fallback: extract the text field from the matched plain-read frame line.
  [ -n "${ans_text:-}" ] || ans_text="$(printf '%s' "$reply_line" | sed -E 's/.*"text": *"(.*)"\}.*/\1/')"
  ans_len=${#ans_text}
  fmt_bad=0
  case "$ans_text" in
    *'**'*|*'__'*) fmt_bad=1 ;;            # bold/italic markdown
  esac
  printf '%s' "$ans_text" | grep -qE '(^|[[:space:]])([#]{1,6}[[:space:]]|[-*][[:space:]])' && fmt_bad=1   # headers / bullets
  echo "  (answer: ${ans_len} chars — \"$ans_text\")"
  { [ "${ans_len:-9999}" -le 250 ] && [ "$fmt_bad" -eq 0 ]; } \
    && ok "BREVITY: answer is <=250 chars, plain text, no formatting (wikilinks ok)" \
    || no "BREVITY: answer broke the bar (${ans_len} chars, fmt_bad=$fmt_bad)"

  echo "== $pass passed, $fail failed =="
  [ "$fail" -eq 0 ] || { KEEP=1; echo "see $P/run.log + $P/{conv,curate,gate}.stdout.jsonl + $P/violet.stderr"; exit 1; }
  echo "violet WARM pseudo-agent validated (hermetic; haiku gate → wakes sonnet deep refresh on significant events only → haiku answers from fresh context; output capture; ≤250c plain answers; no live LLM/bus)."
  exit 0
fi

echo "usage: violet-runtime-warm.sh (demo | live)"; exit 2
