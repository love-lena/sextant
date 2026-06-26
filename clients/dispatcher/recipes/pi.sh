#!/usr/bin/env sh
# The pi-headless recipe for the M5 dispatcher (TASK-178). It is the second
# reference value for `sextant-dispatch --harness` (agent.sh launches a `claude`
# crew member; this launches a HEADLESS pi session). A pi session, run under the
# @sextant/pi-bus extension (TASK-177), is a first-class bus client: it boots, the
# extension opens an SDK client on the CHILD's OWN minted creds, and the worker is
# addressable over the bus (a DM or a watched topic wakes a turn) and observable in
# the dash via its pi.activity stream -- a crew member, headless.
#
# THE SWAPPABLE SEAM (same as agent.sh). The harness is a plain `sh -c CMD` with
# env vars; this recipe is selected by pointing --harness at it. WHAT to do is the
# brief ($SX_PROMPT); WHO the worker is is the child's minted creds (SEXTANT_CREDS).
#
# IDENTITY ISOLATION (ADR-0033 mint-on-behalf, TASK-158). The dispatcher mints the
# child with its OWN authority and sets SEXTANT_CREDS to the CHILD's creds in this
# script's environment. The extension reads SEXTANT_PI_CREDS for its bus identity,
# so we point it at the child's creds. We NEVER pass the operator's or dispatcher's
# ambient creds -- the worker is a co-equal crew member, never an impersonator.
#
# RESUME / RE-SPAWN (the managed handoff, AC#3). pi persists a session as JSONL
# under --session-dir, keyed on a session id. This recipe derives a STABLE session
# id from the child's bus id (pi-<SX_CHILD_ID>), so re-launching this recipe for the
# SAME child RESUMES the persisted session (pi --session-id creates-or-resumes). The
# managed handoff is: a bus pi.handoff{drain} winds the worker down (it announces
# relinquished, drains its bus client -> offline, and exits), the operator resumes
# the JSONL by hand, then the dispatcher re-spawns this recipe -> pi --session-id
# resumes the same session and the extension announces acquired. SINGLE-OWNER: a
# given session id has exactly one live launcher at a time; the relinquish completes
# (worker offline) before any re-spawn, so two processes never fight the session.
#
# Environment the dispatcher provides (see clients/dispatcher/main.go spawn()):
#   SEXTANT_CREDS   the CHILD's own minted creds file  (identity isolation)
#   SEXTANT_STORE   the bus store dir (bus.json discovery)
#   SX_PROMPT       the operator's brief -- the task, the swappable DIRECTION
#   SX_CHILD_ID     the child's bus-minted ULID (the stable session-id seed)
#   SX_CHILD_NICK   the child's chosen name (Haiku auto-named or requested)
#   SX_JOB          optional job/lineage label
# Plus, inherited from the dispatcher's environment (export before launching it):
#   SEXTANT_PI_EXTENSION  path to the built @sextant/pi-bus extension entrypoint
#                         (clients/pi-bus/dist/src/index.js) -- REQUIRED
#   SX_AGENT_MODEL        model for the pi worker (default: claude-haiku-4-5)
#   SX_PI_SESSION_DIR     base dir for session JSONL (default: a stable per-child dir
#                         under $SEXTANT_STORE/pi-sessions so a re-spawn resumes)
#   SX_PI_BIN             the pi binary (default: pi on PATH)
#   SX_PI_RESUME_SESSION  an explicit pi session id to resume (overrides the derived
#                         pi-<child-id>); set by an operator-driven re-spawn that
#                         resumes a specific persisted session
#   ANTHROPIC_API_KEY     the model credential (the worker runs a real model)
set -eu

: "${SEXTANT_CREDS:?the dispatcher must set SEXTANT_CREDS to the childs own creds}"
: "${SEXTANT_STORE:?the dispatcher must set SEXTANT_STORE}"
: "${SEXTANT_PI_EXTENSION:?export SEXTANT_PI_EXTENSION=path/to/clients/pi-bus/dist/src/index.js before the dispatcher}"

MODEL="${SX_AGENT_MODEL:-claude-haiku-4-5}"
CHILD_ID="${SX_CHILD_ID:-unknown}"
PI_BIN="${SX_PI_BIN:-pi}"

# A STABLE session id seeded from the child's bus id, so a re-spawn of the same
# child resumes the same persisted JSONL. An explicit SX_PI_RESUME_SESSION wins (an
# operator resuming a specific session). pi's session id must be alphanumeric / -_.
# and bracket-alphanumeric; a ULID and the "pi-" prefix satisfy that.
SESSION_ID="${SX_PI_RESUME_SESSION:-pi-${CHILD_ID}}"

# A STABLE session dir so the JSONL outlives one process (resume depends on it). A
# SIBLING of the bus store, never INSIDE it: the store is NATS JetStream's data dir,
# and a worker's session log is not bus stream data — a store reset must not wipe
# session history, and the two concerns should not share a directory. Keyed to the
# deployment (not the process), so the dispatcher and a by-hand resume look in the same
# place. Override with SX_PI_SESSION_DIR.
SESSION_DIR="${SX_PI_SESSION_DIR:-$(dirname "$SEXTANT_STORE")/pi-sessions}"
mkdir -p "$SESSION_DIR"

# Resume by explicit FILE PATH when a session for this child already exists, so a
# revive is INDEPENDENT OF THE LAUNCH CWD. pi scopes --session-id by project (the
# launch directory); a path is unambiguous, so the dispatcher resumes the right JSONL
# even if it restarts from a different directory. On the first spawn there is no file
# yet (pi creates <timestamp>_<id>.jsonl in SESSION_DIR), so we fall back to id + dir.
EXISTING_SESSION=$(ls -t "$SESSION_DIR"/*_"$SESSION_ID".jsonl 2>/dev/null | head -1)

# The extension acts on the CHILD's creds (identity isolation). SEXTANT_PI_CREDS is
# the one required value; the bus is discovered from the store's bus.json.
export SEXTANT_PI_CREDS="$SEXTANT_CREDS"
export SEXTANT_BUS_JSON="${SEXTANT_BUS_JSON:-${SEXTANT_STORE}/bus.json}"

# DRAIN-AND-REVIVE (ADR-0045). A dispatcher-spawned worker is a resumable one-shot,
# not a resident process: it does its task, reports, and EXITS once idle, and the
# dispatcher re-spawns it (resuming this same session id) on the next message. The
# extension's auto-drain reuses the managed-handoff wind-down. Default ON for the
# dispatcher recipe; an operator can pin SEXTANT_PI_DRAIN_WHEN_IDLE=0 to keep a
# worker resident instead.
export SEXTANT_PI_DRAIN_WHEN_IDLE="${SEXTANT_PI_DRAIN_WHEN_IDLE:-1}"

# WORKFLOW STEP (ADR-0011 + ADR-0045). When the workflow coordinator dispatches a
# step it appends "WF_EVENTS=<subject> WF_STEP=<id>" to the brief. We lift those into
# the environment so the extension emits the step-done event DETERMINISTICALLY on
# agent_end -- not depending on the model to remember to publish it -- and strip them
# from the brief the model sees so the task reads cleanly. Absent (a plain mobilize or
# a revive), this is a no-op.
INJECT_PROMPT="${SX_PROMPT:-}"
WF_EVENTS=$(printf '%s' "$INJECT_PROMPT" | sed -n 's/.*WF_EVENTS=\([^[:space:]]*\).*/\1/p')
if [ -n "$WF_EVENTS" ]; then
  export SEXTANT_PI_WF_EVENTS="$WF_EVENTS"
  export SEXTANT_PI_WF_STEP="$(printf '%s' "$INJECT_PROMPT" | sed -n 's/.*WF_STEP=\([^[:space:]]*\).*/\1/p')"
  INJECT_PROMPT=$(printf '%s' "$INJECT_PROMPT" | sed 's/[[:space:]]*WF_EVENTS=.*//')
fi

# A light role nudge so a bus DM lands as a task and the worker replies over the bus
# the way a crew member would. The wake injects the bus message; this system prompt
# tells the worker it is a headless crew member and to answer on the bus.
SYS_PROMPT="You are \"${SX_CHILD_NICK:-pi-worker}\", a headless crew member on a sextant collaboration bus with your OWN bus identity (never the operator's). When a bus message reaches you it is a task or a question from a teammate -- do the work, and REPLY over the bus to whoever sent it (their id is in the message) using the sextant_reply tool, or post substantial output as an artifact and announce it. Be concise: headlines on the bus, substance in artifacts. You act under your own minted credentials; never claim to be the operator or another client."

# Launch pi in RPC mode, headless, with the built @sextant/pi-bus extension and the
# stable session id. -ne disables extension DISCOVERY (hermetic) while the explicit
# -e still loads our extension.
#
# RPC mode ends when stdin closes, so this recipe HOLDS STDIN OPEN for the worker's
# whole life -- the worker is long-lived and bus-addressable, woken by frames, not
# by stdin. We wire pi's stdin to a FIFO that is held open for writing on fd 3 and
# NEVER closed, so pi's stdin never sees EOF and the RPC session stays alive. If a
# brief was given ($SX_PROMPT from the spawn.request) we write ONE `prompt` line into
# the FIFO first, so the spawned worker starts on its task; otherwise it boots IDLE
# and a bus frame is the only thing that wakes it (AC#2). The first-prompt line is
# JSON-encoded by node (guaranteed present with pi) so an arbitrary brief is escaped
# safely -- no shell-quoting hazard.
#
# We `exec` pi (replacing this shell) so pi IS the dispatcher's tracked process and
# is reaped cleanly on shutdown -- no orphan, the same clean shape as agent.sh.
FIFO="$(mktemp -u "${TMPDIR:-/tmp}/pi-stdin-XXXXXX")"
mkfifo "$FIFO"
exec 3<>"$FIFO"   # keep a writer open forever so the reader never sees EOF
rm -f "$FIFO"     # unlinked but the open fds keep it alive
if [ -n "$INJECT_PROMPT" ]; then
  SX_INJECT_PROMPT="$INJECT_PROMPT" node -e 'process.stdout.write(JSON.stringify({type:"prompt",message:process.env.SX_INJECT_PROMPT})+"\n")' >&3
fi

# Resume an existing session by PATH (cwd-independent); else create by id + dir. Built
# with `set --` so a session path containing spaces (e.g. ".../Application Support/...")
# stays a single argument.
if [ -n "$EXISTING_SESSION" ]; then
  set -- --session "$EXISTING_SESSION"
else
  set -- --session-id "$SESSION_ID" --session-dir "$SESSION_DIR"
fi
exec "$PI_BIN" --mode rpc \
  --provider anthropic --model "$MODEL" \
  "$@" \
  -ne -e "$SEXTANT_PI_EXTENSION" \
  --append-system-prompt "$SYS_PROMPT" <&3
