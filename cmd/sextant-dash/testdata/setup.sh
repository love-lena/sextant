#!/usr/bin/env bash
# Hermetic setup for the dash VHS recording (TASK-7.5, AC#3 part B). It builds the
# binaries into a temp dir, brings up an embedded bus, enrols the dash identity +
# a couple more for presence, seeds the stream and an artifact, and starts a
# background publisher so the stream is visibly live during the recording. It is
# driven by gallery.tape (which sources it, then launches `sextant dash`), and is
# fully self-contained via $SEXTANT_HOME + --store in a temp dir (the ADR-0021
# hermetic pattern), so a recording never touches the operator's real config.
#
# Usage (from the worktree root, what the tape does):
#   source cmd/sextant-dash/testdata/setup.sh
#   sextant dash            # the alias resolves the active enrolled context
#   teardown                # stop the bus + background jobs (the tape calls it)
set -euo pipefail

# A fresh hermetic root each run: bus store + context home both isolated.
DASH_TMP="$(mktemp -d "${TMPDIR:-/tmp}/sextant-dash.XXXXXX")"
export SEXTANT_HOME="$DASH_TMP/home"
export SEXTANT_STORE="$DASH_TMP/store"
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

# The dash enrols itself (becomes the active context, so `sextant dash` runs bare).
SEXTANT_SELF_NAME=lena sextant clients register --self --kind human --store "$SEXTANT_STORE" >/dev/null

# A couple more identities for presence, kept connected (subscribed) so they show
# online in the directory the presence pane renders.
sextant clients register coordinator-1 --kind coordinator --store "$SEXTANT_STORE" >/dev/null
sextant clients register agent-beta --kind agent --store "$SEXTANT_STORE" >/dev/null
sextant subscribe msg.topic.plan --creds "$SEXTANT_STORE/coordinator-1.creds" --store "$SEXTANT_STORE" >/dev/null 2>&1 &
COORD_PID=$!
sextant subscribe msg.topic.plan --creds "$SEXTANT_STORE/agent-beta.creds" --store "$SEXTANT_STORE" >/dev/null 2>&1 &
BETA_PID=$!

# Seed the stream backlog so the message pane shows history on launch.
publish() { sextant publish msg.topic.plan "{\"\$type\":\"chat.message\",\"text\":\"$1\"}" --creds "$SEXTANT_STORE/coordinator-1.creds" --store "$SEXTANT_STORE" >/dev/null; }
publish "let's get the dash building"
publish "presence + stream + artifact all mount"
publish "eyeball the cockpit"

# Seed the artifact (a document the artifact + detail panes read).
sextant artifact create the-plan '{"$type":"document","title":"The dash plan","body":"## The M4 panes\n\n- **presence** — the clients directory\n- **message stream** — one read-stream plus an optional compose\n- **artifact** — a document reader\n\nDetail-on-demand is toggled, never an always-on column."}' --creds "$SEXTANT_STORE/coordinator-1.creds" --store "$SEXTANT_STORE" >/dev/null

# A background publisher so the stream visibly ticks while recording.
( for i in 1 2 3 4 5 6 7 8 9 10; do sleep 3; publish "heartbeat $i"; done ) &
PUB_PID=$!

teardown() {
  kill "$PUB_PID" "$COORD_PID" "$BETA_PID" 2>/dev/null || true
  kill -INT "$BUS_PID" 2>/dev/null || true
  wait "$BUS_PID" 2>/dev/null || true
  rm -rf "$DASH_TMP"
}
