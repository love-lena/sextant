#!/usr/bin/env bash
# Hermetic setup for the dash VHS recording (ADR-0024). It builds the binaries
# into a temp dir, brings up an embedded bus, registers a couple of identities
# for the clients browser (the dash itself self-enrolls on first run — the
# zero-config launch the recording shows), seeds two topics and an artifact so
# all three browser lists have rows, and starts a background publisher so the
# open conversation is visibly live during the recording. It is driven by
# dash.tape (which sources it, then launches `sextant dash`), and is fully
# self-contained via $SEXTANT_HOME + --store in a temp dir (the ADR-0021
# hermetic pattern), so a recording never touches the operator's real config.
#
# Usage (from the worktree root, what the tape does):
#   source cmd/sextant-dash/testdata/setup.sh
#   sextant dash            # first run: self-enrolls, then opens the cockpit
#   teardown                # stop the bus + background jobs (the tape calls it)
set -euo pipefail

# A fresh hermetic root each run: bus store + context home both isolated.
DASH_TMP="$(mktemp -d "${TMPDIR:-/tmp}/sextant-dash.XXXXXX")"
export SEXTANT_HOME="$DASH_TMP/home"
export SEXTANT_STORE="$DASH_TMP/store"
export SEXTANT_SELF_NAME=lena # the name the dash's first-run enrollment uses
export PATH="$DASH_TMP/bin:$PATH"
mkdir -p "$DASH_TMP/bin" "$SEXTANT_HOME" "$SEXTANT_STORE"

REPO_ROOT="$(git rev-parse --show-toplevel)"
( cd "$REPO_ROOT" && go build -o "$DASH_TMP/bin/sextant" ./cmd/sextant )
( cd "$REPO_ROOT" && go build -o "$DASH_TMP/bin/sextant-dash" ./cmd/sextant-dash )

# Bring the bus up in the background and wait for its discovery file.
sextant up --store "$SEXTANT_STORE" --port 0 >"$DASH_TMP/bus.log" 2>&1 &
BUS_PID=$!
for _ in $(seq 1 100); do
  [ -f "$SEXTANT_STORE/bus.json" ] && break
  sleep 0.1
done
[ -f "$SEXTANT_STORE/bus.json" ] || { echo "bus did not come up" >&2; cat "$DASH_TMP/bus.log" >&2; exit 1; }

# Identities for the clients browser, registered held-mode (the operator mints
# for another). The dash's OWN identity is deliberately NOT enrolled here — the
# recording launches `sextant dash` with no context to show the zero-config
# first run. coordinator-1 is kept connected (subscribed) so it shows online.
sextant clients register coordinator-1 --kind coordinator --store "$SEXTANT_STORE" >/dev/null
sextant clients register agent-beta --kind agent --store "$SEXTANT_STORE" >/dev/null
sextant subscribe msg.topic.plan --creds "$SEXTANT_STORE/coordinator-1.creds" --store "$SEXTANT_STORE" >/dev/null 2>&1 &
COORD_PID=$!

# Seed two topics so the topics browser discovers both from its msg.topic.>
# replay ("plan" sorts first, so the recording opens it).
publish() { sextant publish "msg.topic.$1" "{\"\$type\":\"chat.message\",\"text\":\"$2\"}" --creds "$SEXTANT_STORE/coordinator-1.creds" --store "$SEXTANT_STORE" >/dev/null; }
publish plan "let's get the dash building"
publish plan "the three browsers all mount"
publish standup "agent-beta: widgets landed"

# Seed the artifact the artifacts browser lists and its reader opens.
sextant artifact create the-plan '{"$type":"document","title":"The dash plan","body":"## The three browsers\n\n- **clients** — the directory; Enter opens a DM\n- **topics** — every topic with messages; Enter opens its conversation\n- **artifacts** — the bucket; Enter opens this reader\n\nA detail opens inside the same pane; Esc pops back to the list."}' --creds "$SEXTANT_STORE/coordinator-1.creds" --store "$SEXTANT_STORE" >/dev/null

# A background publisher so the open conversation visibly ticks while recording.
( for i in 1 2 3 4 5 6 7 8 9 10; do sleep 3; publish plan "heartbeat $i"; done ) &
PUB_PID=$!

# Drop the background jobs from job control so bash never paints a "[n]+ Done"
# notification over the recording when one finishes (teardown kills by PID).
disown -a

teardown() {
  kill "$PUB_PID" "$COORD_PID" 2>/dev/null || true
  kill -INT "$BUS_PID" 2>/dev/null || true
  wait "$BUS_PID" 2>/dev/null || true
  rm -rf "$DASH_TMP"
}
