#!/usr/bin/env bash
# One-command demo of the sextant Claude Code plugin (TASK-22).
#
#   clients/claude-code/demo.sh
#
# Stands up a throwaway bus, registers two clients — demo-claude (this
# session's identity) and alice (a CLI peer that auto-replies) — installs the
# plugin from this checkout, and drops you into a Claude Code session wired to
# the bus with channel push enabled.
#
# In the session:
#   1. approve the dev-channel dialog (Enter)
#   2. say:  subscribe to msg.topic.demo and say hello to whoever is there
#   3. watch: the `subscribed` notice injects, claude's hello goes out,
#      and alice's acknowledgment pushes back into the session a beat later.
#
# Exit claude (ctrl-d) and the script prints the topic transcript as alice
# saw it — both directions, bus-stamped authors — then cleans up.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/../.." && pwd)"
PLUGIN_DIR="$REPO/clients/claude-code"
D="$(mktemp -d /tmp/sextant-demo.XXXXXX)"
BIN="$D/bin"
STORE="$D/store"
HOME_CTX="$D/home"
PROJ="$D/proj"
TOPIC="msg.topic.demo"

say() { printf '\033[1;36m[demo]\033[0m %s\n' "$*"; }

say "building sextant + sextant-mcp from $REPO"
mkdir -p "$BIN" "$PROJ/.claude"
(cd "$REPO" && go build -o "$BIN/sextant" ./clients/go/apps/sextant && go build -o "$BIN/sextant-mcp" ./clients/go/apps/mcp)

say "starting a throwaway bus"
"$BIN/sextant" up --store "$STORE" --port 0 >"$D/bus.log" 2>&1 &
BUS_PID=$!
for _ in $(seq 1 100); do [ -f "$STORE/bus.json" ] && break; sleep 0.1; done
[ -f "$STORE/bus.json" ] || { echo "bus did not start; see $D/bus.log" >&2; exit 1; }

say "registering identities: demo-claude (this session) + alice (CLI peer)"
SEXTANT_HOME="$HOME_CTX" USER=demo-claude "$BIN/sextant" clients register --self --store "$STORE" >/dev/null
ALICE_OUT="$("$BIN/sextant" clients register alice --store "$STORE")"
ALICE_ID="$(printf '%s' "$ALICE_OUT" | grep -oE '[0-9A-HJKMNP-TV-Z]{26}' | head -1)"
ALICE_CREDS="$STORE/alice.creds"

# alice: a pull-loop peer (read-since by cursor — no tail pipelines). She
# acknowledges frames that aren't hers — at most 3, so two well-mannered
# agents can't ping-pong acknowledgments forever — and the reviewer sees a
# real cross-client round-trip push back into the session.
alice_responder() {
  local cursor=0 out next id acks=0
  while sleep 2; do
    out="$("$BIN/sextant" read "$TOPIC" --since "$cursor" --limit 50 --creds "$ALICE_CREDS" --store "$STORE" 2>&1)" || continue
    next="$(printf '%s\n' "$out" | sed -n 's/.*next cursor \([0-9][0-9]*\).*/\1/p')"
    while IFS= read -r line; do
      case "$line" in
      "[$TOPIC]"*"<$ALICE_ID>"*) ;; # her own frame
      "[$TOPIC]"*)
        [ "$acks" -ge 3 ] && continue
        acks=$((acks + 1))
        id="$(printf '%s' "$line" | awk '{print $2}')"
        "$BIN/sextant" publish "$TOPIC" \
          "{\"\$type\":\"chat.message\",\"replyTo\":\"$id\",\"text\":\"alice here — got your message ($id) loud and clear\"}" \
          --creds "$ALICE_CREDS" --store "$STORE" >/dev/null 2>&1 || true
        ;;
      esac
    done <<EOF
$out
EOF
    [ -n "$next" ] && cursor="$next"
  done
}
alice_responder &
ALICE_PID=$!

cleanup() {
  kill "$ALICE_PID" "$BUS_PID" 2>/dev/null || true
  wait "$ALICE_PID" "$BUS_PID" 2>/dev/null || true
}
trap cleanup EXIT

say "installing the plugin from this checkout (marketplace: sextant)"
claude plugin marketplace remove sextant >/dev/null 2>&1 || true
claude plugin marketplace add "$PLUGIN_DIR" >/dev/null
claude plugin install sextant@sextant >/dev/null 2>&1 || true

# Pre-allow the plugin's tools in the scratch project so the demo runs
# without permission prompts.
cat >"$PROJ/.claude/settings.json" <<'JSON'
{
  "permissions": {
    "allow": ["mcp__plugin_sextant_sextant"]
  }
}
JSON

say ""
say "claude is starting. In the session:"
say "  1. approve the dev-channel dialog (Enter)"
say "  2. say:  subscribe to msg.topic.demo and say hello to whoever is there"
say "  3. watch the subscribed notice and alice's reply inject as ← sextant events"
say "exit (ctrl-d) for the transcript."
say ""

(cd "$PROJ" && PATH="$BIN:$PATH" SEXTANT_HOME="$HOME_CTX" SEXTANT_STORE="$STORE" \
  claude --dangerously-load-development-channels plugin:sextant@sextant) || true

say ""
say "the topic as alice (the CLI peer) saw it — authors are bus-stamped ULIDs:"
"$BIN/sextant" read "$TOPIC" --creds "$ALICE_CREDS" --store "$STORE" || true
say ""
say "the directory (presence derives from live connections):"
"$BIN/sextant" clients list --creds "$ALICE_CREDS" --store "$STORE" || true
say ""
say "demo state was under $D (removed on exit). The plugin stays installed;"
say "remove with: claude plugin uninstall sextant@sextant && claude plugin marketplace remove sextant"
