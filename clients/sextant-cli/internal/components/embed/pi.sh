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
#   SX_PI_AUTO_ENTRY      path to the pi-auto extension entry the worker loads for
#                         its sandbox + reviewer (TASK-118). Default:
#                         ~/.pi/agent/git/github.com/yonilerner/pi-auto/extensions/pi-auto.ts
#                         (the operator's installed pi-auto). pi-auto reads the
#                         operator's ~/.pi/agent/extensions/pi-auto.json verbatim.
#   SX_PI_WORKDIR / SEXTANT_PI_WORKDIR  the worker's scoped working dir (its CWD).
#                         Default: a per-child dir under the store. pi-auto's
#                         sandbox confines bash writes to this CWD (allowWrite ["."]).
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
# PER-CHILD session dir (under a shared pi-sessions root): each worker gets its
# OWN dir so the sandbox profile can allow-read it WITHOUT exposing sibling
# workers' session transcripts (a shared dir would leak them under the jail).
# Stable per child id, so a re-spawn resumes the same session.
SESSION_DIR="${SX_PI_SESSION_DIR:-$(dirname "$SEXTANT_STORE")/pi-sessions/${CHILD_ID}}"
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
# WORKER SANDBOX (TASK-118). pi is the work engine's SOLE harness (ADR-0052), so
# EVERY dispatched worker is a coding agent with full file + Bash tools. Unscoped
# it ran under launchd in launchd's CWD (/), roamed the operator's filesystem
# (recurring macOS TCC popups), and could reach the GUI/system (the Firefox-close
# scare). One operator-facing flag picks the enforcement posture:
#
#   SX_PI_SANDBOX_MODE = sandbox  (DEFAULT) — the HARD WALL. The whole pi worker
#     runs inside @foxfirecodes/sandbox-runtime (`srt`) with a scoped profile and
#     NO pi-auto in the path: there is no reviewer, so there is no escape. An
#     instructed out-of-scope write / protected read / external egress / GUI or
#     system command is DENIED at the OS layer.
#   SX_PI_SANDBOX_MODE = automode — REGULAR pi-auto. Loads pi-auto with the
#     operator's settings VERBATIM (escape-only + codex-verbatim reviewer): the
#     reviewer-adjudicated, ESCAPABLE mode the operator opts into. Its softer
#     guarantee is intended (it behaves as the operator's interactive pi).
#
# BOTH modes confine the worker's CWD to a per-run scoped dir and FAIL LOUD rather
# than ever spawn an unconfined worker.

SANDBOX_MODE="${SX_PI_SANDBOX_MODE:-sandbox}"
case "$SANDBOX_MODE" in
  sandbox | automode) ;;
  *)
    echo "pi.sh: refusing to spawn — unknown SX_PI_SANDBOX_MODE='$SANDBOX_MODE' (want 'sandbox' or 'automode') (TASK-118 fail-loud)" >&2
    exit 78 # EX_CONFIG
    ;;
esac

# The scoped working dir (the worker's CWD), shared by both modes. Override with
# SEXTANT_PI_WORKDIR / SX_PI_WORKDIR (an operator pinning a real worktree); else a
# per-child dir, a SIBLING of the bus store (never inside it — the store is
# JetStream's data dir).
WORKDIR="${SEXTANT_PI_WORKDIR:-${SX_PI_WORKDIR:-$(dirname "$SEXTANT_STORE")/pi-work/${CHILD_ID}}}"
if [ -z "$WORKDIR" ] || [ "$WORKDIR" = "/" ]; then
  echo "pi.sh: refusing to spawn an UNSCOPED worker — working dir is empty or '/' (TASK-118 fail-loud)" >&2
  exit 78 # EX_CONFIG
fi
if ! mkdir -p "$WORKDIR" 2>/dev/null; then
  echo "pi.sh: refusing to spawn — cannot create scoped working dir '$WORKDIR' (TASK-118 fail-loud)" >&2
  exit 78
fi
export SEXTANT_PI_WORKDIR="$WORKDIR"

# HOME must resolve (to the operator's home, inherited from the launchd-managed
# dispatcher): the worker's model creds live under it, and in automode pi-auto
# reads the operator's ~/.pi/agent/extensions/pi-auto.json + borrows the OpenAI
# reviewer key from pi's auth.json via the model registry, both rooted at
# HOME/.pi/agent. An empty HOME would silently mis-resolve — refuse instead.
: "${HOME:?HOME must be set so the worker resolves its model creds (and pi-auto config in automode) (TASK-118)}"
PI_AGENT_DIR="${PI_AGENT_DIR:-${HOME}/.pi/agent}"

if [ "$SANDBOX_MODE" = "automode" ]; then
  # AUTOMODE: load pi-auto explicitly. The worker launches with `-ne` (discovery
  # off, so it can't pull arbitrary extensions), so pi-auto — which the operator's
  # interactive pi loads via settings.json `packages` discovery — is named with
  # its own `-e` alongside @sextant/pi-bus. pi-auto reads the operator's
  # pi-auto.json itself; we pass NO sandbox settings. FAIL LOUD if the entry is
  # missing or sandbox-exec is absent (never a silently-unsandboxed automode worker).
  PI_AUTO_ENTRY="${SX_PI_AUTO_ENTRY:-${PI_AGENT_DIR}/git/github.com/yonilerner/pi-auto/extensions/pi-auto.ts}"
  if [ ! -f "$PI_AUTO_ENTRY" ]; then
    echo "pi.sh: refusing to spawn (automode) — pi-auto entry not found at '$PI_AUTO_ENTRY' (TASK-118 fail-loud). Install pi-auto (pi package git:github.com/yonilerner/pi-auto) or set SX_PI_AUTO_ENTRY." >&2
    exit 78
  fi
  if [ "$(uname -s)" = "Darwin" ] && ! command -v sandbox-exec >/dev/null 2>&1; then
    echo "pi.sh: refusing to spawn (automode) — sandbox-exec not found but pi-auto's sandbox is configured (TASK-118 fail-loud: configured-but-unavailable)." >&2
    exit 78
  fi
else
  # SANDBOX (DEFAULT): wrap the whole worker in `srt`. Resolve the srt CLI (ships
  # inside pi-auto's node_modules; override with SX_PI_SRT_CLI). FAIL LOUD if the
  # runtime is unavailable — never run an unconfined worker.
  SRT_CLI="${SX_PI_SRT_CLI:-${PI_AGENT_DIR}/git/github.com/yonilerner/pi-auto/node_modules/@foxfirecodes/sandbox-runtime/dist/cli.js}"
  if [ ! -f "$SRT_CLI" ]; then
    echo "pi.sh: refusing to spawn (sandbox) — srt runtime not found at '$SRT_CLI' (TASK-118 fail-loud: sandbox runtime unavailable). Install pi-auto (which vendors @foxfirecodes/sandbox-runtime) or set SX_PI_SRT_CLI, or run with SX_PI_SANDBOX_MODE=automode." >&2
    exit 78
  fi
  if [ "$(uname -s)" = "Darwin" ] && ! command -v sandbox-exec >/dev/null 2>&1; then
    echo "pi.sh: refusing to spawn (sandbox) — sandbox-exec not found but the OS sandbox is required (TASK-118 fail-loud: sandbox runtime unavailable)." >&2
    exit 78
  fi

  # Build the srt profile. The hard wall via srt's settings schema:
  #   filesystem.allowWrite = the scope + the session dir (the worker's JSONL lives
  #     OUTSIDE the scope, a sibling of the store) → everything else write-DENIED.
  #   filesystem.denyRead   = the operator's sensitive paths AND the GUI/system
  #     command binaries — reads default-allow, so we DENYLIST the protected paths
  #     (a worker can't read ~/.ssh etc.) and the dangerous binaries (it can't exec
  #     osascript/killall/open/shutdown/sudo if it can't read them).
  #   network.allowedDomains = the model API only (api.anthropic.com); the egress
  #     allow-list runs through srt's CONNECT proxy (no MITM CA needed). Everything
  #     else is denied.
  #   network.allowLocalBinding = true → permits the LOOPBACK NATS bus connection
  #     (raw TCP, not proxyable) while keeping external egress denied. NOTE: this
  #     covers a loopback bus (the operator's setup). A REMOTE/LAN bus host is NOT
  #     reachable under this profile (raw TCP, non-loopback) — front it with a
  #     loopback forwarder, or use automode, if a remote bus is ever needed.
  #   allowAppleEvents=false (capability-layer GUI denial, defense in depth beyond
  #     the binary denylist) — a worker cannot drive apps via AppleEvents.
  : "${SX_PI_MODEL_DOMAIN:=api.anthropic.com}"
  SRT_PROFILE="${WORKDIR}/.sx-srt-settings.json"
  # pi keeps a lock + session + cache state in its agent config dir; the operator's
  # ~/.pi/agent is NOT writable under the jail (and holds the operator's auth.json
  # keys we must not expose), so we point pi at a SCOPED per-worker config dir
  # inside the scope and DENY-READ the operator's auth.json. In sandbox mode the
  # worker uses its OWN model credential from ANTHROPIC_API_KEY (env) — there is no
  # pi-auto reviewer needing the operator's OpenAI key — so the scoped config dir
  # is self-sufficient and the worker never touches the operator's pi state.
  PI_WORKER_AGENT_DIR="${WORKDIR}/.pi-agent"
  mkdir -p "$PI_WORKER_AGENT_DIR" 2>/dev/null || {
    echo "pi.sh: refusing to spawn (sandbox) — cannot create scoped pi config dir '$PI_WORKER_AGENT_DIR' (TASK-118 fail-loud)" >&2
    exit 78
  }
  export PI_CODING_AGENT_DIR="$PI_WORKER_AGENT_DIR"

  # The srt profile (the hard wall). srt reads default-ALLOW, so we DENYLIST the
  # operator's sensitive paths — the shell rc/dotfiles, credential stores, the
  # private user dirs, pi's own auth.json (the operator API keys). We deny-read
  # these specific trees rather than the WHOLE home, because the worker's
  # toolchain (node/pi + the @sextant/pi-bus extension's deps) and its own bus
  # creds/session live under paths that a blanket home-deny starves. allowWrite =
  # the scope + the session dir (sibling of the store) + the scoped pi config dir.
  # Extra sensitive paths can be appended via SX_PI_SRT_DENY_READ (space-separated).
  #
  # GUI / DESTRUCTIVE containment is NOT done by denying the command binaries —
  # denyRead of /usr/bin/osascript does NOT block EXEC (it still runs). The real
  # containment is the capabilities: allowAppleEvents=false denies app control
  # (osascript/open -a cannot drive or launch apps) and srt's same-sandbox signal
  # restriction means a worker cannot kill HOST processes (killall/pkill of the
  # operator's apps fails). The binary denyRead entries below are harmless
  # defense-in-depth (they remove the tools from sight), not the load-bearing wall.
  SX_SRT_CREDS_FILE="$SEXTANT_CREDS" SX_SRT_CREDS_DIR="$(dirname "$SEXTANT_CREDS")" \
  SX_SRT_BUSJSON_FILE="$SEXTANT_BUS_JSON" SX_SRT_STORE="$SEXTANT_STORE" \
  SX_SRT_EXTRA_DENY="${SX_PI_SRT_DENY_READ:-}" \
  SX_SRT_WORKDIR="$WORKDIR" SX_SRT_SESSIONDIR="$SESSION_DIR" SX_SRT_PICFG="$PI_WORKER_AGENT_DIR" \
  SX_SRT_HOME="$HOME" SX_SRT_DOMAIN="$SX_PI_MODEL_DOMAIN" \
    node -e '
      const j = (o) => JSON.stringify(o);
      const home = process.env.SX_SRT_HOME;
      // Sensitive home paths a dispatched worker must never read. Shell rc/dotfiles
      // (they can carry tokens + reveal the environment), credential stores, the
      // private user dirs, and pi/cloud config holding keys.
      const denyRead = [
        home + "/.zshrc", home + "/.zshenv", home + "/.zprofile", home + "/.bashrc",
        home + "/.bash_profile", home + "/.profile", home + "/.bash_history", home + "/.zsh_history",
        home + "/.ssh", home + "/.aws", home + "/.gnupg", home + "/.kube", home + "/.docker",
        home + "/.config", home + "/.gitconfig", home + "/.git-credentials",
        home + "/.netrc", home + "/.npmrc", home + "/.pypirc",
        // High-value credential stores (qa-306): SSH certs to PROD infra, agent +
        // cloud + registry creds. A denylist is inherently incomplete, but these
        // KNOWN stores must never ship readable to a dispatched worker.
        home + "/.tsh", // Teleport SSH certs (production infrastructure)
        home + "/.codex", // OpenAI codex creds (auth.json under it)
        home + "/.claude", home + "/.claude.json", home + "/.claude.json.backup", // Claude Code config / MCP secrets
        home + "/.cloudflared", // Cloudflare tunnel creds
        home + "/.cargo/credentials", home + "/.cargo/credentials.toml", home + "/.gem", home + "/.cursor",
        home + "/.pi/agent/auth.json", // the operator OpenAI/Anthropic keys — never exposed
        home + "/Documents", home + "/Desktop", home + "/Downloads", home + "/Movies",
        home + "/Pictures", home + "/Library/Keychains", home + "/Library/Application Support",
        home + "/Library/Cookies", home + "/Library/Safari", home + "/Library/Messages",
        home + "/Library/Containers", home + "/Library/Group Containers",
        // Defense-in-depth only (does NOT block exec — see header): hide the GUI/
        // system command binaries. Real containment = allowAppleEvents:false +
        // same-sandbox signal restriction.
        "/usr/bin/osascript", "/usr/bin/killall", "/usr/bin/pkill", "/usr/bin/open",
        "/sbin/shutdown", "/sbin/reboot", "/usr/bin/sudo", "/bin/launchctl", "/usr/bin/automator",
      ];
      for (const p of (process.env.SX_SRT_EXTRA_DENY || "").split(/\s+/).filter(Boolean)) denyRead.push(p);
      // ISOLATE SIBLINGS. The dispatcher writes EVERY child creds file as
      // <id>.creds into ONE shared workdir, and the bus store holds other agent
      // state, so DENY-read those shared dirs and allow-read ONLY this worker OWN
      // creds FILE + the bus.json FILE (allowRead overrides denyRead for an exact
      // path). Without this a sandbox worker could read a sibling creds file and
      // impersonate it. The session dir is PER-CHILD (see SESSION_DIR), so it
      // carries no sibling transcripts and is allow-read whole.
      denyRead.push(process.env.SX_SRT_CREDS_DIR); // shared creds dir (sibling <id>.creds)
      denyRead.push(process.env.SX_SRT_STORE);     // the bus store (other agent state)
      // The worker OWN readable paths (allowRead overrides the denies above):
      // its scope, its per-child session dir, its scoped pi config, its OWN creds
      // FILE, and the bus.json FILE (bus discovery). Files, not parent dirs.
      const allowRead = [];
      for (const p of [
        process.env.SX_SRT_WORKDIR, process.env.SX_SRT_SESSIONDIR, process.env.SX_SRT_PICFG,
        process.env.SX_SRT_CREDS_FILE, process.env.SX_SRT_BUSJSON_FILE,
      ]) {
        if (p && !allowRead.includes(p)) allowRead.push(p);
      }
      const settings = {
        filesystem: {
          allowRead,
          allowWrite: [process.env.SX_SRT_WORKDIR, process.env.SX_SRT_SESSIONDIR, process.env.SX_SRT_PICFG],
          denyRead,
          denyWrite: [],
        },
        network: {
          allowedDomains: [process.env.SX_SRT_DOMAIN],
          deniedDomains: [],
          strictAllowlist: true,
          allowLocalBinding: true, // raw-TCP LOOPBACK egress for the NATS bus (proxy is HTTP-only)
          allowAppleEvents: false, // capability-layer GUI denial (no AppleScript-driven apps)
        },
      };
      process.stdout.write(j(settings));
    ' > "$SRT_PROFILE" || {
    echo "pi.sh: refusing to spawn (sandbox) — could not write srt profile to '$SRT_PROFILE' (TASK-118 fail-loud)" >&2
    exit 78
  }
fi
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

# CONFINE the worker to its scoped dir: cd here BEFORE exec so pi's tools (and
# pi-auto's sandbox, allowWrite ["."]) default to the scope, never launchd's CWD.
# FAIL LOUD — a worker that cannot enter its scope must not run unscoped.
cd "$WORKDIR" || {
  echo "pi.sh: refusing to spawn — cannot cd into scoped working dir '$WORKDIR' (TASK-118 fail-loud)" >&2
  exit 78
}
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
# Build pi's argv in $@. The session args come first (resume by PATH when one
# exists — cwd-independent; else create by id + dir).
if [ -n "$EXISTING_SESSION" ]; then
  set -- --session "$EXISTING_SESSION"
else
  set -- --session-id "$SESSION_ID" --session-dir "$SESSION_DIR"
fi
# Common pi flags. -ne disables extension DISCOVERY so the worker loads ONLY the
# extensions we name with -e (never an arbitrary discovered one). The @sextant/
# pi-bus extension (bus identity + wake) is always loaded.
set -- --mode rpc --provider anthropic --model "$MODEL" "$@" -ne -e "$SEXTANT_PI_EXTENSION"
# AUTOMODE additionally loads pi-auto (its sandbox + reviewer, the escapable mode).
# SANDBOX mode does NOT load pi-auto — the hard OS wall (srt, below) is the whole
# enforcement, with no reviewer to escape.
if [ "$SANDBOX_MODE" = "automode" ]; then
  set -- "$@" -e "$PI_AUTO_ENTRY"
fi
set -- "$@" --append-system-prompt "$SYS_PROMPT"

# Launch. We `exec` (replacing this shell) so the worker IS the dispatcher's
# tracked process and is reaped cleanly on shutdown — no orphan.
if [ "$SANDBOX_MODE" = "sandbox" ]; then
  # SANDBOX (DEFAULT): run the WHOLE worker inside srt with the scoped profile.
  # srt applies a sandbox-exec jail to pi AND every descendant, so there is no
  # reviewer and no escape: an instructed out-of-scope op is OS-denied. srt passes
  # stdin/stdout/stderr through, so the FIFO-on-stdin RPC framing is preserved.
  exec node "$SRT_CLI" -s "$SRT_PROFILE" -- "$PI_BIN" "$@" <&3
else
  # AUTOMODE: bare pi with pi-auto loaded (reviewer-adjudicated, escapable).
  exec "$PI_BIN" "$@" <&3
fi
