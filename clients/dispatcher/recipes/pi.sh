#!/usr/bin/env sh
# The pi-headless recipe for the dispatcher (TASK-178) — pi is the work engine's
# SOLE harness (ADR-0052; the earlier claude recipe is removed). It is the
# reference value for `sextant-dispatch --harness`: it launches a HEADLESS pi
# session as the spawned worker. A pi session, run under the
# @sextant/pi-bus extension (TASK-177), is a first-class bus client: it boots, the
# extension opens an SDK client on the CHILD's OWN minted creds, and the worker is
# addressable over the bus (a DM or a watched topic wakes a turn) and observable in
# the dash via its pi.activity stream -- a crew member, headless.
#
# THE SWAPPABLE SEAM. The harness is a plain `sh -c CMD` with
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

# ---------------------------------------------------------------------------
# WORKER SANDBOX (TASK-118). pi is the work engine's SOLE harness, so EVERY
# dispatched worker is a coding agent with full file + Bash tools. Left
# unscoped it launches under launchd in launchd's CWD (/), roams the operator's
# filesystem (the recurring macOS TCC popups), and can reach the GUI/system (the
# Firefox-close scare). We confine the worker before it ever runs a tool:
#
#   1. A per-run SCOPED WORKING DIR under the store, set as the worker's CWD, so
#      its file tools default there and never the operator's home. FAIL LOUD if
#      it cannot be resolved/created — never an unscoped worker (AC#1, AC#5).
#   2. A SHELL-LEVEL command guard: a bin dir of wrapper scripts, prepended to
#      PATH, that REFUSE the destructive/GUI/system command classes (killall,
#      pkill, osascript, open, brew/npm/pip installs, shutdown, git force-push)
#      with a clear error. This mirrors the wf-release-pr wrapper: enforcement
#      at the shell, so a worker TOLD to run them still cannot (AC#2). It is
#      defense in depth for `sh -c` subshells the in-process gate cannot parse;
#      the pi-bus tool_call gate (gate.ts) is the primary, path-aware layer.
#
# This is the floor, not the whole defense — the real isolation for an untrusted
# unattended agent is the OS boundary (a container/VM). The scope raises the
# floor so the default path is least-privilege.

# The scoped working dir. Override with SEXTANT_PI_WORKDIR (an operator pinning a
# real worktree for an agentic-dev run); otherwise a per-child dir, a SIBLING of
# the bus store (never inside it — the store is JetStream's data dir). Keyed to
# the child id so each worker gets its own scratch and a re-spawn reuses it.
WORKDIR="${SEXTANT_PI_WORKDIR:-$(dirname "$SEXTANT_STORE")/pi-work/${CHILD_ID}}"
# FAIL LOUD: an empty scope (e.g. CHILD_ID unset AND no override resolving to a
# real path) must never yield an unscoped worker. We refuse to spawn.
if [ -z "$WORKDIR" ] || [ "$WORKDIR" = "/" ]; then
  echo "pi.sh: refusing to spawn an UNSCOPED worker — SEXTANT_PI_WORKDIR is empty (TASK-118 fail-loud)" >&2
  exit 78 # EX_CONFIG
fi
if ! mkdir -p "$WORKDIR" 2>/dev/null; then
  echo "pi.sh: refusing to spawn — cannot create scoped working dir '$WORKDIR' (TASK-118 fail-loud)" >&2
  exit 78
fi
# The confinement ROOT the in-process gate enforces file tools against; the dir
# the worker is CONFINED to is its CWD below. Exported so gate.ts reads it.
export SEXTANT_PI_WORKDIR="$WORKDIR"

# Install the shell-level command guard: a bin dir of refuse-wrappers prepended
# to PATH. Each wrapper exits non-zero with a clear message, so a worker that
# shells out to a denied class is blocked at the shell (not by playbook), even
# inside a `sh -c` subshell. A real `git` still works; only `git push --force`
# (without --force-with-lease) is refused, mirroring gate.ts.
GUARD_BIN="${WORKDIR}/.sx-guard-bin"
if ! mkdir -p "$GUARD_BIN" 2>/dev/null; then
  echo "pi.sh: refusing to spawn — cannot install command guard at '$GUARD_BIN' (TASK-118 fail-loud)" >&2
  exit 78
fi
# _SX_REAL_FN is the body of a _sx_real shell function, inlined into each
# passthrough wrapper. It resolves the REAL binary for a name by scanning PATH
# with the guard dir removed (a plain `command -v` would re-find the wrapper,
# since the guard dir is first on PATH). Defined once here, embedded literally.
_SX_REAL_FN='_sx_real() {
  _n="$1"; _oldifs="$IFS"; IFS=:
  for _d in $PATH; do
    [ "$_d" = "$GUARD_BIN" ] && continue
    if [ -x "$_d/$_n" ]; then IFS="$_oldifs"; printf "%s\\n" "$_d/$_n"; return 0; fi
  done
  IFS="$_oldifs"; return 1
}'

# A plain refuse-wrapper for a whole-command class. $0 is the wrapper's name so
# one body serves every name we symlink/copy it under.
_sx_refuse() {
  cat > "$GUARD_BIN/$1" <<'GUARD'
#!/usr/bin/env sh
echo "sextant worker sandbox (TASK-118): '$(basename "$0")' is DENIED for dispatched workers (destructive/GUI/system command class). This is a shell-level guard; it cannot be bypassed by the worker." >&2
exit 126
GUARD
  chmod +x "$GUARD_BIN/$1"
}
for _cmd in killall pkill osascript shutdown halt reboot launchctl; do
  _sx_refuse "$_cmd"
done
# `open` (macOS app/file launcher — the Firefox-close vector) and package
# installers get their own refuse-wrappers too. A bare `open`/`brew`/etc. is the
# GUI/install class; refuse the binary outright (a worker has no business
# launching apps or installing software).
for _cmd in open brew port; do
  _sx_refuse "$_cmd"
done
# Package managers: refuse only the INSTALL subcommands so a worker can still
# `npm test` / `pip --version` in its tree but cannot install software. The
# passthrough re-execs the REAL binary, found by resolving the command against a
# PATH with the guard dir REMOVED — `command -v` honours PATH order, so a plain
# lookup would just find this wrapper again and loop. _sx_real (defined once,
# inlined into each wrapper) does the guard-stripped lookup.
_sx_refuse_subcmd() {
  # $1 = command, rest = denied first-arg subcommands
  _name="$1"; shift
  _deny="$*"
  cat > "$GUARD_BIN/$_name" <<GUARD
#!/usr/bin/env sh
GUARD_BIN="$GUARD_BIN"
$_SX_REAL_FN
for d in $_deny; do
  if [ "\$1" = "\$d" ]; then
    echo "sextant worker sandbox (TASK-118): '$_name \$1' is DENIED for dispatched workers (package install). Shell-level guard." >&2
    exit 126
  fi
done
exec "\$(_sx_real "$_name")" "\$@"
GUARD
  chmod +x "$GUARD_BIN/$_name"
}
_sx_refuse_subcmd npm install i add ci
_sx_refuse_subcmd pip install
_sx_refuse_subcmd pip3 install
_sx_refuse_subcmd gem install
_sx_refuse_subcmd cargo install
# git: allow everything EXCEPT a force-push without --force-with-lease (the
# history-clobbering class, mirroring gate.ts). Re-exec the real git otherwise.
cat > "$GUARD_BIN/git" <<GUARD
#!/usr/bin/env sh
GUARD_BIN="$GUARD_BIN"
$_SX_REAL_FN
_real_git="\$(_sx_real git)"
if [ "\$1" = "push" ]; then
  for a in "\$@"; do
    case "\$a" in
      --force-with-lease*) exec "\$_real_git" "\$@" ;;
      --force|-f|--force=*) echo "sextant worker sandbox (TASK-118): 'git push --force' is DENIED (use --force-with-lease). Shell-level guard." >&2; exit 126 ;;
    esac
  done
fi
exec "\$_real_git" "\$@"
GUARD
chmod +x "$GUARD_BIN/git"
export PATH="$GUARD_BIN:$PATH"

# CONFINE the worker's CWD to the scoped dir: pi's file tools (read/bash/edit)
# default to the launch CWD, so launching here is the first half of confinement
# (the gate.ts path check is the enforced half). cd FAILS LOUD — a worker that
# cannot enter its scope must not run from wherever launchd left us.
cd "$WORKDIR" || {
  echo "pi.sh: refusing to spawn — cannot cd into scoped working dir '$WORKDIR' (TASK-118 fail-loud)" >&2
  exit 78
}
# ---------------------------------------------------------------------------

# DRAIN-AND-REVIVE (ADR-0045). A dispatcher-spawned worker is a resumable one-shot,
# not a resident process: it does its task, reports, and EXITS once idle, and the
# dispatcher re-spawns it (resuming this same session id) on the next message. The
# extension's auto-drain reuses the managed-handoff wind-down. Default ON for the
# dispatcher recipe; an operator can pin SEXTANT_PI_DRAIN_WHEN_IDLE=0 to keep a
# worker resident instead.
export SEXTANT_PI_DRAIN_WHEN_IDLE="${SEXTANT_PI_DRAIN_WHEN_IDLE:-1}"

# WORKFLOW RUN STEP (ADR-0048 + ADR-0045). When the run executor dispatches a step it
# appends "RUN_EVENTS=<subject> RUN_STEP=<id>" to the brief. We lift those into the
# environment so the extension emits the step-done run.event DETERMINISTICALLY on
# agent_end -- not depending on the model to remember to publish it -- and strip them
# from the brief the model sees so the task reads cleanly. Absent (a plain mobilize or a
# revive), this is a no-op. (RUN_EVENTS/RUN_STEP precede RUN_STEP on one line, so the
# strip from RUN_EVENTS to end removes both.)
INJECT_PROMPT="${SX_PROMPT:-}"
RUN_EVENTS=$(printf '%s' "$INJECT_PROMPT" | sed -n 's/.*RUN_EVENTS=\([^[:space:]]*\).*/\1/p')
if [ -n "$RUN_EVENTS" ]; then
  export SEXTANT_PI_RUN_EVENTS="$RUN_EVENTS"
  export SEXTANT_PI_RUN_STEP="$(printf '%s' "$INJECT_PROMPT" | sed -n 's/.*RUN_STEP=\([^[:space:]]*\).*/\1/p')"
  INJECT_PROMPT=$(printf '%s' "$INJECT_PROMPT" | sed 's/[[:space:]]*RUN_EVENTS=.*//')
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
# is reaped cleanly on shutdown -- no orphan.
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
