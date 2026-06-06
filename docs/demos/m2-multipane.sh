#!/usr/bin/env bash
# Drives a four-pane tmux view of the M2 collaboration loop for the VHS recording
# (docs/demos/m2-collaboration-loop.tape). It is the multi-pane counterpart of the
# acceptance scenario (tests/e2e/m2-acceptance.md): each client gets its own pane,
# so you watch deliveries land LIVE — alice publishes/writes in her panes and the
# frames appear in bob's subscribe/watch panes in real time.
#
# Layout:
#   ┌ operator — issue + directory ─┬ bob — subscribe msg.topic.plan ┐
#   ├ alice — publish + artifact ───┼ bob — watch the-plan ──────────┤
#
# The script creates the session + panes + bus, then sends timed commands to each
# pane; the tape attaches and records. Run standalone to preview:
#   SEX=/tmp/sxdemo/sextant tmux ... ; bash docs/demos/m2-multipane.sh ; tmux attach -t m2demo
set -uo pipefail

SEX="${SEX:-/tmp/sxdemo/sextant}"          # the built `sextant` binary
S="${S:-/tmp/sxdemo/store}"                # bus store dir
SESH="${SESH:-m2demo}"                      # tmux session name
BIN_DIR="$(cd "$(dirname "$SEX")" && pwd)"

# Fresh store + a bus (background). Record its pid so the tape can stop it.
rm -rf "$S"; mkdir -p "$S"
"$SEX" up --store "$S" --port 4400 >"$S/up.log" 2>&1 &
echo $! >"$S/bus.pid"
for _ in $(seq 1 80); do [ -f "$S/bus.json" ] && break; sleep 0.1; done

# A clean 2x2 session. Capture pane ids so layout never depends on index math.
tmux kill-session -t "$SESH" 2>/dev/null || true
OP=$(tmux new-session -d -P -F '#{pane_id}' -s "$SESH" -x 220 -y 52 "bash --norc")
SUB=$(tmux split-window -h  -t "$OP"  -P -F '#{pane_id}' "bash --norc")   # right-top
ALI=$(tmux split-window -v  -t "$OP"  -P -F '#{pane_id}' "bash --norc")   # left-bottom
WAT=$(tmux split-window -v  -t "$SUB" -P -F '#{pane_id}' "bash --norc")   # right-bottom
tmux select-layout -t "$SESH" tiled

tmux set -t "$SESH" -g status off
tmux set -t "$SESH" -g pane-border-status top
tmux set -t "$SESH" -g pane-border-format " #[bold]#{pane_title} "

tmux select-pane -t "$OP"  -T "operator  —  issue + directory"
tmux select-pane -t "$ALI" -T "alice  —  publish + artifact"
tmux select-pane -t "$SUB" -T "bob  —  subscribe msg.topic.plan"
tmux select-pane -t "$WAT" -T "bob  —  watch the-plan"

# Each pane: a clean prompt + the env the typed commands rely on ($S, $ALICE, $BOB).
for P in "$OP" "$ALI" "$SUB" "$WAT"; do
  tmux send-keys -t "$P" "export PATH='$BIN_DIR':\$PATH S='$S' ALICE='$S/alice.creds' BOB='$S/bob.creds'; PS1='\$ '; clear" Enter
done

run() { tmux send-keys -t "$1" "$2" Enter; }

sleep 3.5   # let the tape attach and show the clean four-pane layout first

# --- the operator issues both identities (held-identity + enrollment modes) ---
run "$OP"  "# the bus is the sole minter — operator issues, keys stay in the bus"
sleep 1.3
run "$OP"  "sextant clients register alice --kind worker --store \$S"
sleep 2.2
run "$OP"  "sextant clients register --self --kind reviewer --name bob --store \$S"
sleep 2.6

# --- bob subscribes; alice publishes; the frame lands LIVE in bob's pane → ---
run "$SUB" "sextant subscribe msg.topic.plan --creds \$BOB --store \$S"
sleep 1.9
run "$ALI" "# alice publishes -- watch it appear in bob's subscribe pane, top-right"
sleep 1.3
run "$ALI" "sextant publish msg.topic.plan '{\"hello\":\"world\"}' --creds \$ALICE --store \$S"
sleep 3.2

# --- a shared artifact; bob watches; alice's update lands LIVE in bob's pane → ---
run "$ALI" "sextant artifact create the-plan '{\"title\":\"v1\"}' --creds \$ALICE --store \$S"
sleep 2.2
run "$WAT" "sextant artifact watch the-plan --creds \$BOB --store \$S"
sleep 2.6
run "$ALI" "sextant artifact update the-plan '{\"title\":\"v2\"}' --rev 1 --creds \$ALICE --store \$S"
sleep 3.2

# --- the live directory: both online, presence derived from the connection ---
run "$OP"  "sextant clients list --creds \$ALICE --store \$S"
sleep 4.5
