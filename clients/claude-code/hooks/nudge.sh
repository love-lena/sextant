#!/usr/bin/env bash
# sextant Stop/SubagentStop nudge hook
#
# Fires on every Stop and SubagentStop event. When this session is connected to
# the bus (i.e. the MCP server wrote a per-session identity file), it injects a
# soft reminder as additionalContext asking the agent to check three things
# before stopping:
#
#   1. SUBSCRIPTIONS — am I subscribed to / caught up on the subjects I should
#      be following?
#   2. MESSAGES — have I posted to the bus what the crew/operator needs (status,
#      decisions, hand-offs)?
#   3. REVIEW-FLAGS — have I marked artifacts / PRs that need the operator's
#      judgment as review.state=review?
#
# This is a SOFT nudge — it emits additionalContext only and never sets
# "decision": "block". The agent reads the reminder and acts on it (or confirms
# already-done); the turn ends normally. A hook that always blocks would trap the
# agent in a loop.
#
# Discipline (matches attest/status): exits 0 on every path, NEVER blocks or
# hangs the turn. When the bus is unreachable or the session has no identity
# (a regular non-bus session), the hook exits 0 with no output — transparent.
#
# Hook contract (Stop / SubagentStop command hook, Claude Code):
#   stdin  — JSON with session_id, transcript_path, cwd, hook_event_name
#   stdout — JSON: { "hookSpecificOutput": { "hookEventName": "...",
#                                            "additionalContext": "..." } }
#   exit   — always 0; diagnostics to stderr

# Guard: never let an unexpected error propagate out; degrade to silent exit 0.
set -uo pipefail
trap 'exit 0' ERR

# --------------------------------------------------------------------------- #
# Require jq — used for both input parsing and JSON-safe output encoding.     #
# jq ships with macOS (via Homebrew) and all major Linux distros; if it is    #
# absent we degrade silently rather than produce malformed JSON.              #
# --------------------------------------------------------------------------- #
if ! command -v jq >/dev/null 2>&1; then
  echo "sextant nudge: jq not found; skipping nudge" >&2
  exit 0
fi

# --------------------------------------------------------------------------- #
# Guard: only run when this session is connected to the bus.                  #
# The MCP server writes a per-session identity file under CLAUDE_PLUGIN_DATA  #
# (keyed on CLAUDE_CODE_SESSION_ID) on every connect.  If the file is absent  #
# the MCP server never connected — a regular non-bus session — so we exit 0   #
# silently, exactly as attest and status do.                                  #
# --------------------------------------------------------------------------- #
DATA_DIR="${CLAUDE_PLUGIN_DATA:-}"
SESSION_ID="${CLAUDE_CODE_SESSION_ID:-}"

if [[ -z "$DATA_DIR" || -z "$SESSION_ID" ]]; then
  exit 0
fi

IDENTITY_FILE="${DATA_DIR}/${SESSION_ID}.identity.json"
if [[ ! -f "$IDENTITY_FILE" ]]; then
  exit 0
fi

# --------------------------------------------------------------------------- #
# Read the hook event name from stdin (so we can echo it back correctly).     #
# A missing or garbled stdin is survivable — default to Stop.                 #
# --------------------------------------------------------------------------- #
stdin_data="$(cat)"
HOOK_EVENT="$(printf '%s' "$stdin_data" | jq -r '.hook_event_name // "Stop"' 2>/dev/null || echo "Stop")"

# --------------------------------------------------------------------------- #
# Emit the soft nudge.                                                        #
# --------------------------------------------------------------------------- #
NUDGE_TEXT="[sextant — pre-stop check] Before finishing this turn, verify:

1. SUBSCRIPTIONS — Are you subscribed to every subject you should be following? If you just started a new workstream or were asked to monitor a topic, call message_subscribe now so you don't miss the next message.

2. MESSAGES — Have you posted what the crew and operator need to see? That includes: a status update (message_publish to your crew topic or the relevant workflow topic), any decisions you made, and any hand-off information the next agent will need. If the work is done, say so on the bus.

3. REVIEW-FLAGS — Have you marked every artifact or PR that needs the operator's judgment as ready for review? Call artifact_update with review_state=review on any item you want Lena to look at before proceeding.

If you've already done all three, you're clear to stop. If not, take care of the outstanding items first."

# Output the hook JSON.  hookEventName must match the triggering event.
# jq -Rsc . JSON-encodes the multiline string safely (raw input, slurp, compact).
printf '{"hookSpecificOutput":{"hookEventName":%s,"additionalContext":%s}}\n' \
  "$(printf '%s' "$HOOK_EVENT" | jq -Rsc .)" \
  "$(printf '%s' "$NUDGE_TEXT" | jq -Rsc .)"
