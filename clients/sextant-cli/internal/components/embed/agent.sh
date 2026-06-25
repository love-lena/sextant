#!/usr/bin/env sh
# The DEFAULT capable, self-directing agent recipe for the M5 dispatcher.
#
# This is the reference value for `sextant-dispatch --harness`: a one-shot script
# that stands up a REAL, bus-connected `claude` agent under the CHILD's own minted
# identity, hands it the operator's prompt as its task, and gives it a light
# self-direction role prompt so a VAGUE brief ("write the v0.7.0 press release")
# becomes "gather the context that tells me what that means, then do it".
#
# THE SWAPPABLE SEAM (design constraint). The dispatcher's harness is a plain
# `sh -c CMD` with env vars — this recipe is the DEFAULT, never a hardcode. Two
# things are independently swappable:
#   * the RECIPE — point --harness at a different script to change WHAT is
#     mobilized (e.g. a future "run workflow X" recipe);
#   * the DIRECTION — $SX_PROMPT is the task INPUT. A future workflow mode slots
#     in by handing a detailed goal as the prompt ("implement this goal: <goal>")
#     with no change to this harness. We do NOT build workflow-mode here; we just
#     don't foreclose it. ROLE_PROMPT below is overridable via $SX_ROLE_PROMPT for
#     exactly this reason.
#
# IDENTITY ISOLATION (TASK-158, non-negotiable). The agent acts as ITSELF, never
# the operator or the dispatcher. The dispatcher mints the child and sets
# SEXTANT_CREDS to the CHILD's creds in this script's environment; we point the
# sextant MCP server at *those* creds via the MCP config's env block. We NEVER
# pass the operator's or dispatcher's ambient creds to claude. sextant-mcp's
# resolve() honours SEXTANT_CREDS / SEXTANT_CONTEXT first, so the agent connects
# under its own ULID.
#
# Environment the dispatcher provides (see cmd/sextant-dispatch/main.go spawn()):
#   SEXTANT_CREDS   the CHILD's own minted creds file  (identity isolation)
#   SEXTANT_STORE   the bus store dir (bus.json discovery)
#   SX_PROMPT       the operator's brief — the task, the swappable DIRECTION
#   SX_CHILD_ID     the child's bus-minted ULID
#   SX_CHILD_NICK   the child's chosen name (Haiku auto-named or requested)
#   SX_JOB          optional job/lineage label
# Plus, inherited from the dispatcher's environment (export before launching it):
#   SEXTANT_MCP_BIN path to the sextant-mcp binary (required)
#   SX_AGENT_MODEL  model for the agent (default: claude-sonnet-4-6 — capable)
#   SX_ROLE_PROMPT  override the self-direction role prompt (swappable direction)
#   SX_OPERATOR     display name of who mobilized the agent (for "DM the operator")
set -eu

: "${SEXTANT_CREDS:?the dispatcher must set SEXTANT_CREDS to the childs own creds}"
: "${SEXTANT_STORE:?the dispatcher must set SEXTANT_STORE}"
: "${SEXTANT_MCP_BIN:?export SEXTANT_MCP_BIN=path/to/sextant-mcp before the dispatcher}"

MODEL="${SX_AGENT_MODEL:-claude-sonnet-4-6}"
NICK="${SX_CHILD_NICK:-agent}"
OPERATOR="${SX_OPERATOR:-the operator who mobilized you}"

# MCP config pins sextant-mcp at the CHILD's creds (identity isolation). The env
# block here is what the MCP server inherits; SEXTANT_CREDS = the child's file.
MCP="$(mktemp)"
printf '{"mcpServers":{"sextant":{"command":"%s","env":{"SEXTANT_CREDS":"%s","SEXTANT_STORE":"%s"}}}}' \
  "$SEXTANT_MCP_BIN" "$SEXTANT_CREDS" "$SEXTANT_STORE" > "$MCP"

# The self-direction role prompt: the agent has its OWN bus identity, gathers
# context from artifacts + messages, DMs the operator if the brief is vague, then
# does the work. This is the DEFAULT direction; $SX_ROLE_PROMPT overrides it so a
# different mode (e.g. a workflow goal) swaps in without touching the harness.
# Built via a quoted heredoc (literal text; only the two named placeholders are
# substituted afterwards) to keep the prose free of shell-quoting hazards.
ROLE_FILE="$(mktemp)"
trap 'rm -f "$MCP" "$ROLE_FILE"' EXIT
cat > "$ROLE_FILE" <<'ROLE'
You are "__NICK__", an autonomous agent with your OWN identity on the sextant collaboration bus. You were just mobilized to do a piece of work. You have the sextant MCP tools: list and read artifacts, read and subscribe to message subjects, publish messages, and DM other clients -- all as YOURSELF (your own bus identity, never anyone elses).

How to work:
1. Read your brief below. If it names an artifact, a goal, a topic, or a person, that is your starting thread.
2. GATHER CONTEXT before acting: list the artifacts, read the ones relevant to the brief, and skim the recent messages on related subjects. A short brief usually points at context that defines it -- find that context and let it tell you what "done" means.
3. If after gathering context the brief is still genuinely ambiguous -- you cannot tell what is being asked or what good output looks like -- DM __OPERATOR__ with ONE crisp clarifying question, then proceed on your best reading rather than stalling. A DM is a two-way conversation: publish a chat.message to subject msg.topic.dm.<their-id>.<your-id> (the two client ids in either order; find their id via the clients list, yours is your own bus id).
4. Do the work. Produce the deliverable the brief calls for (an artifact for substantial output; a message for a short answer or a status update). Announce what you did on the bus.
5. Be a good bus citizen: terse messages (headlines, about one line), substance goes in artifacts you link by name.

You act under your own minted credentials. Never claim to be the operator or another client.
ROLE
# Substitute the two named placeholders (no eval; plain literal replace).
DEFAULT_ROLE_PROMPT=$(sed -e "s/__NICK__/$NICK/g" -e "s/__OPERATOR__/$OPERATOR/g" "$ROLE_FILE")

ROLE_PROMPT="${SX_ROLE_PROMPT:-$DEFAULT_ROLE_PROMPT}"

BRIEF="${SX_PROMPT:-(no brief was provided -- DM the operator to ask what they need)}"
PROMPT="$ROLE_PROMPT

--- YOUR BRIEF ---
$BRIEF
--- END BRIEF ---

Begin now. Gather context first, then do the work."

# --strict-mcp-config + the explicit --mcp-config keep the agent on EXACTLY the
# child-scoped sextant server (no inherited operator MCP). The agent is capable:
# it gets the sextant tools and may read/write files, but not a blanket bypass.
exec claude -p "$PROMPT" \
  --model "$MODEL" \
  --strict-mcp-config --mcp-config "$MCP" \
  --permission-mode acceptEdits \
  --allowedTools "mcp__sextant__artifact_list,mcp__sextant__artifact_get,mcp__sextant__artifact_create,mcp__sextant__artifact_update,mcp__sextant__message_read,mcp__sextant__message_subscribe,mcp__sextant__message_publish,mcp__sextant__clients_list,Read,Glob,Grep" \
  --output-format json </dev/null
