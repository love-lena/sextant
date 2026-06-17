#!/usr/bin/env bash
# Agentic dev workflow — run harness + token-free plumbing demo (TASK-98).
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
#                                           # editing agents). Opens a PR to main; never merges.
#
#   agentic-dev-workflow.sh run-v05 "<task>"# LIVE v0.5 VARIANT: same workflow, but it targets
#                                           # the v0.5 integration branch — worktree off
#                                           # origin/v0.5, PR base v0.5, and the release GATE
#                                           # pings the PSEUDO-OPERATOR (sirius), not the
#                                           # principal. The workflow still only OPENS a PR (the
#                                           # gh/git shims still refuse merge/push/tag); sirius
#                                           # merges to v0.5 separately. Dangerous/irreversible
#                                           # steps still escalate to the REAL principal (Lena).
#
# The orchestration logic lives in the orchestrator's playbook (an LLM), NOT here — this
# is setup + the wf-* helper tools the orchestrator calls.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
MODE="${1:-demo}"
TASK="${2:-}"

# sirius — the v0.5 pseudo-operator (the gate approver for the v0.5 variant). Overridable
# (e.g. WF_PSEUDO_OPERATOR=<orion's id> to gate orion instead). This is a *bus client id*,
# not the principal — WF_PRINCIPAL stays the real principal for dangerous-escalation gates.
SIRIUS_ID="01KTYFK00J6RXP4CFPHPWRBRS1"

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

  # wf-dm "<text>" — DM the GATE PEER (headline only). In the default run that's the
  # principal; in the v0.5 variant it's the pseudo-operator (sirius) — see WF_DM.
  cat >"$bin/wf-dm" <<'EOF'
#!/usr/bin/env sh
t="$("$WF_BIN/_wf-esc" "$1")"
"$WF_SEXTANT" publish "$WF_DM" \
  "{\"\$type\":\"chat.message\",\"text\":\"$t\"}" \
  --creds "$WF_ORCH_CREDS" --store "$SEXTANT_STORE" >/dev/null 2>&1
EOF

  # wf-dm-principal "<text>" — DM the REAL principal (escalation gate), regardless of variant.
  # Use ONLY for dangerous/irreversible escalations (merge to main, tag, force-push, history
  # rewrite, other repos, destructive, credentials). Falls back to the gate-peer DM if the
  # principal DM isn't set (the demo/main run, where they're the same peer).
  cat >"$bin/wf-dm-principal" <<'EOF'
#!/usr/bin/env sh
t="$("$WF_BIN/_wf-esc" "$1")"
to="${WF_PRINCIPAL_DM:-$WF_DM}"
"$WF_SEXTANT" publish "$to" \
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
    # claude worker: edit+bash scoped to the worktree. Capture the session id so a sticky
    # step (wf-spawn-resume) can actually resume THIS agent — without this, resume silently
    # falls back to a fresh agent and stickiness is a no-op (caught by the dogfood review).
    out="$(claude -p "$prompt" --model "${WF_CLAUDE_MODEL:-claude-haiku-4-5}" \
      --strict-mcp-config --mcp-config "$mcp" --add-dir "$WF_WORKTREE" \
      --permission-mode acceptEdits \
      --allowedTools "Read,Edit,Write,Bash,mcp__sextant__message_publish,mcp__sextant__artifact_create,mcp__sextant__artifact_update" \
      --output-format json </dev/null)"
    printf '%s' "$out" | jq -r '.session_id // empty' > "$WF_WORKERS/$role.session" 2>/dev/null || true
    printf '%s' "$out" | jq -r '.result // .text // empty' ;;
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
# Resume the prior claude session if we captured a NON-EMPTY one (-s, not -f: an empty
# session file would make --resume "" error); else fall back to a fresh turn.
if [ -s "$sid" ]; then
  claude -p "$prompt" --resume "$(cat "$sid")" --model "${WF_CLAUDE_MODEL:-claude-haiku-4-5}" \
    --strict-mcp-config --mcp-config "$mcp" --add-dir "$WF_WORKTREE" \
    --permission-mode acceptEdits \
    --allowedTools "Read,Edit,Write,Bash,mcp__sextant__message_publish,mcp__sextant__artifact_create,mcp__sextant__artifact_update" \
    --output-format text </dev/null
else
  "$WF_BIN/wf-spawn" "$role" claude "$promptfile"
fi
EOF

  # Open-PR guardrail (TASK-98): a `gh` shim, first on the orchestrator's + workers' PATH,
  # that refuses `gh ... merge` so the workflow OPENs a PR but never merges it — a shell-level
  # guardrail (defense in depth over playbook compliance), not an OS sandbox. `gh pr create`
  # and read-only gh pass straight through to the real binary (captured before $WF_BIN is on PATH).
  realgh="$(command -v gh 2>/dev/null || echo gh)"
  cat >"$bin/gh" <<EOF
#!/usr/bin/env sh
case " \$* " in
  *" merge "*) echo "wf-guard: refusing 'gh ... merge' — the agentic workflow opens PRs, it never merges (release guard)" >&2; exit 3 ;;
esac
exec "$realgh" "\$@"
EOF

  # Destructive-git guardrail (TASK-118 worker least-privilege + TASK-122): a `git` shim
  # on the same PATH that refuses the release-path ops the playbook forbids — force-push
  # (incl. a +refspec), push to main/master, and tag — for the orchestrator AND its workers
  # (their Bash inherits this PATH). This is a guardrail against an OVER-EAGER COOPERATIVE
  # worker, NOT a sandbox: a full-path /usr/bin/git or a PATH reorder bypasses a shell shim;
  # true least-privilege is OS-level (container/seccomp), the eventual evolution. Normal
  # add/commit/status/diff and a non-force feature-branch push pass straight through.
  # Captured before $WF_BIN is on PATH so the shim calls the real git.
  realgit="$(command -v git 2>/dev/null || echo git)"
  cat >"$bin/git" <<EOF
#!/usr/bin/env sh
# the first non-flag token is the git subcommand.
sub=""; for a in "\$@"; do case "\$a" in -*) ;; *) sub="\$a"; break ;; esac; done
if [ "\$sub" = push ]; then
  case " \$* " in
    *" --force "*|*" -f "*|*" --force-with-lease"*|*" --mirror "*|*" --delete "*|*" -d "*|*" +"*)
      echo "wf-guard: refusing destructive 'git push' (force / +refspec / mirror / delete) — open a PR, never rewrite (release guard)" >&2; exit 3 ;;
  esac
  case " \$* " in
    *" main "*|*" master "*|*":main "*|*":master "*|*" +main"*|*" +master"*)
      echo "wf-guard: refusing 'git push' to main/master — the workflow opens a PR from a feature branch (release guard)" >&2; exit 3 ;;
  esac
fi
if [ "\$sub" = tag ]; then
  echo "wf-guard: refusing 'git tag' — the workflow never tags releases (release guard)" >&2; exit 3
fi
exec "$realgit" "\$@"
EOF

  # wf-release-pr <pr create args...> — the ONLY sanctioned release operation (TASK-122).
  # It runs `gh pr create` and nothing else; any other verb is refused, so the release
  # STEP has a single auditable door that can only OPEN a PR (never merge/push/tag).
  # If WF_PR_BASE is set (the v0.5 variant: WF_PR_BASE=v0.5) and the caller didn't pass an
  # explicit --base, the wrapper injects it — so the variant opens to v0.5 even if the
  # orchestrator omits the flag (defense in depth; the default main run leaves base to gh).
  cat >"$bin/wf-release-pr" <<'EOF'
#!/usr/bin/env sh
if [ "$1" = pr ] && [ "$2" = create ]; then
  shift 2
  if [ -n "${WF_PR_BASE:-}" ]; then
    case " $* " in
      *" --base "*|*" -B "*) ;;                 # caller set the base explicitly — respect it
      *) set -- --base "$WF_PR_BASE" "$@" ;;     # default to the variant's base (v0.5)
    esac
  fi
  exec gh pr create "$@"
fi
echo "wf-release-pr: refused — this wrapper only runs 'gh pr create' (open a PR; never merge/push/tag/force). Got: $*" >&2
exit 3
EOF

  chmod +x "$bin"/wf-* "$bin"/_wf-esc "$bin/gh" "$bin/git"
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
  # The dash JS bundles are generated, not committed (TASK-121), and embedded by the Go build
  # (go:embed in internal/dashapi). Generate them first or `go build ./cmd/sextant` fails on a
  # fresh checkout. Best-effort: if esbuild/npx is unavailable, fall through and let go build
  # report the missing embed.
  if [ -x "$ROOT/scripts/build-dash-ui.sh" ]; then
    ( cd "$ROOT" && bash scripts/build-dash-ui.sh ) >"$P/dash-ui.log" 2>&1 || echo "  (dash UI build emitted warnings; see $P/dash-ui.log)"
  fi
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

  echo "== shell-enforced autonomy guards (TASK-122 wf-release-pr + TASK-118 worker least-priv) =="
  # A guard refuses with exit 3 + a 'wf-guard'/'wf-release-pr: refused' message; these run
  # on the SAME PATH a worker's Bash inherits, so they bind the workers too.
  guard_blocks(){ gb="$1"; shift; o="$("$@" 2>&1)"; rc=$?; if [ "$rc" = 3 ] && printf '%s' "$o" | grep -qE 'wf-guard|wf-release-pr: refused'; then ok "$gb"; else no "$gb (rc=$rc out=$o)"; fi; }
  guard_allows(){ ga="$1"; shift; o="$("$@" 2>&1)"; if printf '%s' "$o" | grep -qE 'wf-guard|wf-release-pr: refused'; then no "$ga (wrongly refused: $o)"; else ok "$ga"; fi; }
  guard_blocks "git shim refuses force-push"          git push --force origin feature
  guard_blocks "git shim refuses +refspec force-push" git push origin +feature:feature
  guard_blocks "git shim refuses push to main"        git push origin main
  guard_blocks "git shim refuses git tag"             git tag v9.9.9
  guard_blocks "gh shim refuses gh pr merge"          gh pr merge 123
  guard_blocks "wf-release-pr refuses non-create"     wf-release-pr pr merge 123
  guard_allows "git shim allows a normal add"         git add -A
  guard_allows "git shim allows feature-branch push"  git push origin my-feature
  guard_allows "wf-release-pr allows pr create"       wf-release-pr pr create --title x --body y

  echo "== v0.5 variant wiring (token-free inspection: base, PR base → v0.5, gate → sirius) =="
  # 1) `run-v05` mode sets the variant config: WF_BASE=origin/v0.5, WF_PR_BASE=v0.5,
  #    WF_PSEUDO_OPERATOR=sirius. Exercise the SAME mode-prelude the live run uses (no claude),
  #    in a subshell so it can't leak into the demo's env.
  ( MODE=run-v05
    : "${WF_BASE:=origin/v0.5}"; : "${WF_PR_BASE:=v0.5}"; : "${WF_PSEUDO_OPERATOR:=$SIRIUS_ID}"; : "${WF_VARIANT:=v05}"
    [ "$WF_BASE" = origin/v0.5 ] && [ "$WF_PR_BASE" = v0.5 ] && [ "$WF_PSEUDO_OPERATOR" = "$SIRIUS_ID" ] && [ "$WF_VARIANT" = v05 ]
  ) && ok "run-v05 sets base=origin/v0.5, PR base=v0.5, gate peer=sirius ($SIRIUS_ID)" \
     || no "run-v05 variant config wrong"

  # 2) the routine gate DM resolves to the PSEUDO-OPERATOR (sirius), not the principal —
  #    the GATE_PEER selection the run path makes when WF_PSEUDO_OPERATOR is set.
  ( ORCH_ID="ZZZORCH"; PRINCIPAL="AAAPRIN"; WF_PSEUDO_OPERATOR="$SIRIUS_ID"
    GATE_PEER="$PRINCIPAL"; [ -n "$WF_PSEUDO_OPERATOR" ] && GATE_PEER="$WF_PSEUDO_OPERATOR"
    [ "$GATE_PEER" = "$SIRIUS_ID" ] && [ "$GATE_PEER" != "$PRINCIPAL" ]
  ) && ok "routine gate peer = sirius (pseudo-operator), distinct from the principal" \
     || no "gate peer did not redirect to the pseudo-operator"

  # 3) wf-release-pr injects --base v0.5 when WF_PR_BASE is set and the caller omits --base
  #    (defense in depth: the variant opens to v0.5 even if the orchestrator forgets the flag).
  #    Use a fake `gh` (in $FAKEGH, FIRST on PATH) that just echoes its args — so the wrapper's
  #    `exec gh pr create …` resolves to it and we can read what base it passed (no real PR).
  FAKEGH="$P/fakegh"; mkdir -p "$FAKEGH"
  cat >"$FAKEGH/gh" <<'GHEOF'
#!/usr/bin/env sh
echo "GH-ARGS: $*"
GHEOF
  chmod +x "$FAKEGH/gh"
  inj="$(WF_PR_BASE=v0.5 PATH="$FAKEGH:$WF_BIN:$PATH" "$WF_BIN/wf-release-pr" pr create --title x --body y 2>&1)"
  printf '%s' "$inj" | grep -q -- '--base v0.5' \
    && ok "wf-release-pr injects '--base v0.5' under WF_PR_BASE (opens the PR to v0.5)" \
    || no "wf-release-pr did not inject --base v0.5 (got: $inj)"
  # and it RESPECTS an explicit base (never double-injects).
  resp="$(WF_PR_BASE=v0.5 PATH="$FAKEGH:$WF_BIN:$PATH" "$WF_BIN/wf-release-pr" pr create --base main --title x 2>&1)"
  [ "$(printf '%s' "$resp" | grep -o -- '--base' | wc -l | tr -d ' ')" = 1 ] \
    && ok "wf-release-pr respects an explicit --base (no double-inject)" \
    || no "wf-release-pr double-injected --base (got: $resp)"

  # 4) the gh-merge shim STILL refuses merge under the variant (the workflow never merges to v0.5).
  guard_blocks "v0.5 variant: gh shim still refuses 'gh pr merge'" gh pr merge 7 --merge

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
# release = open a PR (stubbed here; the live run does `wf-release-pr pr create`). Prove the path:
"$WF_BIN/wf-event" "release: would run: wf-release-pr pr create (open PR, no merge/tag/force)"
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
# `run` targets main; `run-v05` is the v0.5 variant (worktree off origin/v0.5, PR base v0.5,
# gate → the pseudo-operator). The variant is pure CONFIG: it sets WF_BASE, WF_PSEUDO_OPERATOR,
# WF_PR_BASE, and the variant playbook, then falls through the SAME run path. WF_PRINCIPAL stays
# the real principal in both — only the routine release gate is redirected to the pseudo-operator.
if [ "$MODE" = run-v05 ]; then
  : "${WF_BASE:=origin/v0.5}"; export WF_BASE                 # worktree + PR base = v0.5
  : "${WF_PR_BASE:=v0.5}"; export WF_PR_BASE                  # `gh pr create --base v0.5`
  : "${WF_PSEUDO_OPERATOR:=$SIRIUS_ID}"; export WF_PSEUDO_OPERATOR  # gate → sirius's DM (override for orion)
  : "${WF_VARIANT:=v05}"; export WF_VARIANT                   # selects the v0.5 playbook below
  MODE=run
fi
if [ "$MODE" = run ]; then
  [ -n "$TASK" ] || { echo "usage: agentic-dev-workflow.sh (run | run-v05) \"<task>\""; exit 2; }
  : "${SEXTANT_STORE:?set SEXTANT_STORE to the live bus store}"
  SX="$(command -v sextant)"; SXMCP="$(command -v sextant-mcp)"; SXPOC="${SXPOC:-}"
  [ -n "$SX" ] || { echo "sextant not on PATH"; exit 2; }
  command -v claude >/dev/null || { echo "claude not on PATH"; exit 2; }
  command -v codex  >/dev/null || { echo "codex not on PATH"; exit 2; }
  if [ -z "$SXPOC" ]; then ( cd "$ROOT" && go build -o /tmp/spawn-poc ./cmd/spawn-poc ) && SXPOC=/tmp/spawn-poc; fi

  WF_ID="${WF_ID:-wf$(date +%s 2>/dev/null || echo run)}"
  WT="$ROOT/.claude/worktrees/$WF_ID"
  # Feature-branch prefix: the v0.5 variant brands its branches so they're obviously
  # v0.5-bound; the default (main-targeting) run keeps `agentic/`.
  BRANCH_PREFIX="agentic"; [ "${WF_VARIANT:-}" = v05 ] && BRANCH_PREFIX="agentic-v05"
  echo "== isolated worktree + branch (base ${WF_BASE:-origin/main}) =="
  git -C "$ROOT" worktree add "$WT" -b "$BRANCH_PREFIX/$WF_ID" "${WF_BASE:-origin/main}" || { echo "worktree add failed"; exit 2; }

  echo "== register the orchestrator (top-level; uses your active context) =="
  # OUTSIDE the worktree, so a worker's `git add -A` can never stage the orchestrator's
  # creds/scratch into the branch (and thence a public PR).
  WORKERS="${TMPDIR:-/tmp}/sextant-wf/$WF_ID"; mkdir -p "$WORKERS"
  "$SX" clients register "orchestrator-$WF_ID" --kind agent --store "$SEXTANT_STORE" --out "$WORKERS/orch.creds" >/dev/null
  ORCH_ID="$("$SX" clients list --store "$SEXTANT_STORE" --creds "$WORKERS/orch.creds" | awk -v r="orchestrator-$WF_ID" '$0 ~ r {print $1}' | head -1)"
  PRINCIPAL="$("$SX" principal get --store "$SEXTANT_STORE" --creds "$WORKERS/orch.creds" 2>/dev/null | grep -oE '01[0-9A-HJKMNP-TV-Z]{24}' | head -1)"
  [ -n "$PRINCIPAL" ] || { echo "could not read principal"; exit 2; }

  # The routine release gate pings the GATE PEER. Default = the principal. The v0.5 variant
  # (WF_PSEUDO_OPERATOR set) redirects ONLY this routine gate to the pseudo-operator (sirius),
  # whose authority is scoped to v0.5-PR-open. WF_PRINCIPAL stays the REAL principal — the
  # playbook still escalates anything dangerous/irreversible to a separate principal gate.
  GATE_PEER="$PRINCIPAL"
  if [ -n "${WF_PSEUDO_OPERATOR:-}" ]; then
    GATE_PEER="$WF_PSEUDO_OPERATOR"
    echo "== v0.5 variant: routine gate → pseudo-operator $GATE_PEER (escalation still → principal $PRINCIPAL) =="
  fi
  if [ "$GATE_PEER" \< "$ORCH_ID" ]; then DM="msg.topic.dm.$GATE_PEER.$ORCH_ID"; else DM="msg.topic.dm.$ORCH_ID.$GATE_PEER"; fi
  # The principal-escalation DM (dangerous/irreversible steps) — always the REAL principal,
  # distinct from the routine gate DM above. The playbook posts here for an escalation gate.
  if [ "$PRINCIPAL" \< "$ORCH_ID" ]; then PRINCIPAL_DM="msg.topic.dm.$PRINCIPAL.$ORCH_ID"; else PRINCIPAL_DM="msg.topic.dm.$ORCH_ID.$PRINCIPAL"; fi

  export SEXTANT_STORE WF_ID WF_DM="$DM" WF_TASK="$TASK" WF_WORKTREE="$WT" WF_PRINCIPAL="$PRINCIPAL"
  export WF_PRINCIPAL_DM="$PRINCIPAL_DM"
  export WF_SEXTANT="$SX" WF_SEXTANT_MCP="$SXMCP" WF_ORCH_CREDS="$WORKERS/orch.creds" WF_WORKERS="$WORKERS"
  export WF_STATE="$WORKERS/progress.tsv" WF_BIN="$WORKERS/bin"
  gen_helpers "$WF_BIN"; export PATH="$WF_BIN:$PATH"

  # The v0.5 variant uses an APPEND playbook on top of the generic one: the generic executor
  # plus the variant deltas (pseudo-operator gate, PR base v0.5, dangerous→principal escalation).
  export WF_PLAYBOOK="$ROOT/docs/demos/agentic-dev-workflow-orchestrator.md"
  export WF_VARIANT_PLAYBOOK=""
  if [ "${WF_VARIANT:-}" = v05 ]; then
    export WF_VARIANT_PLAYBOOK="$ROOT/docs/demos/agentic-dev-workflow-v05-orchestrator.md"
  fi
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
# Variant deltas (v0.5): a second appended playbook layered over the generic executor.
[ -n "${WF_VARIANT_PLAYBOOK:-}" ] && common="$common --append-system-prompt-file $WF_VARIANT_PLAYBOOK"
if [ -s "$WF_SESSION" ]; then
  # resume turn: the supervisor loop woke us with $SX_WAKE_TEXT (a gate control, or a
  # "continue" nudge when the prior turn ended mid-pipeline). -s (non-empty), not -f:
  # an empty session file would make --resume "" error.
  claude -p "$SX_WAKE_TEXT" --resume "$(cat "$WF_SESSION")" $common --output-format text </dev/null
else
  # first turn: drive the pipeline from the task + the pipeline file; capture the session
  # id (robust jq parse) so subsequent supervisor turns can --resume this orchestrator.
  out="$(claude -p "Task: $WF_TASK. Your pipeline is in the file $WF_PIPELINE - read it first, then execute it step by step per your playbook." $common --output-format json </dev/null)"
  printf '%s' "$out" > "$WF_TURN1"
  printf '%s' "$out" | jq -r '.session_id // empty' > "$WF_SESSION" 2>/dev/null || true
fi
EOF
  chmod +x "$WORKERS/orch-turn.sh"

  echo "== launch the orchestrator under a resilient supervisor loop =="
  echo "   workflow id: $WF_ID   worktree: $WT   DM: $DM"

  # run_state inspects the workflow's observable state after an orchestrator turn:
  #   done    — a DONE event was emitted (the release step opened the PR); stop.
  #   gate    — the run artifact shows a step awaiting-approval; wait for the human control.
  #   running — the turn ended mid-pipeline (e.g. ran out of turn budget); resume to continue.
  # This is the turn-RESILIENCE fix: the dogfood stalled because the orchestrator drove the
  # whole pipeline in ONE claude -p turn and the turn ended mid-pipeline. The loop re-invokes
  # --resume to carry on across turns, yielding to the human only at a real gate.
  run_state() {
    "$SX" read "msg.workflow.$WF_ID.events" --since 0 --store "$SEXTANT_STORE" --creds "$WORKERS/orch.creds" 2>/dev/null \
      | grep -q '"note":"DONE' && { echo done; return; }
    "$SX" artifact get "$WF_ID.run" --json --store "$SEXTANT_STORE" --creds "$WORKERS/orch.creds" 2>/dev/null \
      | grep -q 'awaiting-approval' && { echo gate; return; }
    echo running
  }

  # wait_for_control blocks until a control lands on the control subject and returns its text
  # (approve / changes <feedback>). spawn-poc --once is the proven wake mechanism.
  wait_for_control() {
    : > "$WORKERS/control.txt"
    "$SXPOC" --creds "$WORKERS/orch.creds" --store "$SEXTANT_STORE" --agent "$ORCH_ID" \
      --watch "msg.workflow.$WF_ID.control" --once \
      --on-wake "printf '%s' \"\$SX_WAKE_TEXT\" > $WORKERS/control.txt" --deadline 24h >/dev/null 2>&1
    cat "$WORKERS/control.txt" 2>/dev/null
  }

  WAKE=""                              # input for the next turn ("" = first turn, uses the task)
  MAX_TURNS="${WF_MAX_TURNS:-40}"      # safety cap so a confused orchestrator can't loop forever
  turn=0
  while [ "$turn" -lt "$MAX_TURNS" ]; do
    turn=$((turn + 1))
    SX_WAKE_TEXT="$WAKE" "$WORKERS/orch-turn.sh"
    case "$(run_state)" in
      done) echo "supervisor: workflow done after $turn turn(s)"; break ;;
      gate)
        echo "supervisor: gate open — awaiting approve/changes on msg.workflow.$WF_ID.control"
        WAKE="$(wait_for_control)"
        echo "supervisor: control received; resuming" ;;
      running)
        echo "supervisor: turn $turn ended mid-pipeline; resuming to continue"
        WAKE="Continue the workflow from where you left off. Re-read $WF_PIPELINE for the steps and the $WF_ID.run artifact for progress, then carry on." ;;
    esac
  done
  [ "$turn" -ge "$MAX_TURNS" ] && echo "supervisor: hit MAX_TURNS=$MAX_TURNS — stopping (possible loop; inspect $WF_ID.run)"
fi

echo "usage: agentic-dev-workflow.sh (demo | run \"<task>\" | run-v05 \"<task>\")"; exit 2
