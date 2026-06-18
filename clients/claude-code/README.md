# sextant — the Claude Code plugin

Makes a Claude Code session a first-class sextant client (ADR-0028): the bus
verbs as MCP tools under one verified identity, inbound messages pushed into
the session as channel events, a skill teaching the conventions, and an
auth/signing hook (ADR-0030) that stamps inbound messages with their verified
author and a trust level.

## The trust hook (ADR-0030)

A `UserPromptSubmit` hook (`hooks/hooks.json` → `sextant-mcp attest`) runs on
each woken turn. It reads new inbound messages on this session's own inbox
(`msg.client.<self>`) and its principal DM (the 2-party topic
`msg.topic.dm.<sorted ids>`, ADR-0034), stamps each by its unforgeable
bus-stamped **author ULID** with a trust level —
**principal** (operator-equivalent), **verified peer** (cooperate, not obey), or
**unknown** (untrusted data) — and delivers them as **trusted, unwrapped**
`additionalContext`, so a validated message never reaches the agent under the
harness's untrusted-channel wrapper. Trust is the ULID alone, never message
content: an operator-styled task from a non-principal ULID is a peer, never the
principal. A per-session cursor (under `CLAUDE_PLUGIN_DATA`, keyed on
`CLAUDE_CODE_SESSION_ID`) makes each message deliver once and survive `--resume`.

Set the bus's principal with `sextant principal set <ulid>` (operator-only).
The hook degrades silently (no injected context, never blocks the turn) on any
bus error and is bounded well under the hard 30s `UserPromptSubmit` timeout.

## The Stop-nudge hook

`Stop` and `SubagentStop` hooks (`hooks/hooks.json` → `hooks/nudge.sh`) run
when any sextant agent finishes a turn. The hook injects a **soft reminder** as
`additionalContext` — it never sets `"decision": "block"`, so the agent stops
normally after considering it. The nudge asks the agent to verify three things
before ending:

1. **Subscriptions** — subscribed to every subject it should be following?
2. **Messages** — posted status, decisions, and hand-off information to the bus?
3. **Review flags** — marked artifacts and PRs that need the operator's judgment
   as `review_state=review`?

`SubagentStop` is included so spawned workers (workflow steps, agentic
subagents) get the same reminder — making this universal across the whole
crew, not just the top-level session.

The hook degrades silently (no output, exit 0) when the session has no bus
identity (a non-bus session) or when `jq` is unavailable. It never blocks a
turn.

**Verify the hook fires:**
```bash
# Simulate a Stop event with an identity file present
mkdir -p /tmp/test-nudge && touch /tmp/test-nudge/mysession.identity.json
CLAUDE_PLUGIN_DATA=/tmp/test-nudge CLAUDE_CODE_SESSION_ID=mysession \
  bash clients/claude-code/hooks/nudge.sh \
  <<< '{"hook_event_name":"Stop"}' | jq '.hookSpecificOutput.additionalContext'
```

## Demo

```bash
clients/claude-code/demo.sh
```

Throwaway bus, two identities, a CLI peer that auto-replies, and a live
session with channel push on. Follow the three printed steps; exit for the
bus-side transcript.

## Install

The [root quickstart](../../README.md#quickstart) has the setup: binaries on
PATH, a reachable bus (`sextant up`), then the marketplace add + install. The
session provisions its own per-session bus identity automatically (ADR-0029) —
no agent `register` step. The GitHub add clones with your git credential
helper, so `gh` auth covers the private repo. Offline, or from an unpacked release tarball, add
this directory instead: `claude plugin marketplace add ./clients/claude-code`
(keep the `./` — a bare `a/b` parses as a GitHub repo).

Tools work everywhere. The channel push path is a Claude Code research
preview behind an allowlist — start sessions with

```bash
claude --dangerously-load-development-channels plugin:sextant@sextant
```

Without the flag the harness drops pushed events silently; the skill's
verification step (the `subscribed` notice after `message_subscribe`) catches
that, and `message_read` polling is the fallback. Pin a per-project identity
with `SEXTANT_CONTEXT` in the project's `.mcp.json` `env` block.

## Updating to a new version

Updating is two independent pieces — the **binaries** (Homebrew) and the
**plugin** (skills, hooks, MCP wiring; Claude Code) — plus restarting the
long-lived processes, because none of them reload in place.

1. **Binaries.** `sextant update` (wraps `brew update && brew upgrade
   love-lena/sextant/sextant`) installs the new `sextant`, `sextant-mcp`, and
   `sextant-dash`. Confirm with `sextant version`.
2. **The bus service.** A `brew services` bus keeps running the *old* binary
   until restarted: `brew services restart sextant`. (Running it in the
   foreground with `sextant up`? Stop and rerun it instead.)
3. **The plugin.** Skills, hooks, and the MCP tool surface ship in the plugin,
   not the formula, so the brew upgrade does not touch them. Pull the new plugin
   version: `/plugin` → manage → update (or `claude plugin marketplace update
   sextant && claude plugin update sextant@sextant`).
4. **Active Claude Code sessions.** A session spawns its `sextant-mcp` at startup
   and keeps using that process — an upgrade does not swap a running server.
   Restart the session (exit and relaunch `claude
   --dangerously-load-development-channels plugin:sextant@sextant`) so it spawns
   the new MCP server and loads the updated skills and trust hook.
5. **The web dash.** `sextant dash --serve` is long-lived too: stop it (Ctrl-C)
   and rerun it to serve the new UI over the new binary.

## Layout

- `.claude-plugin/plugin.json` — the plugin manifest
- `.claude-plugin/marketplace.json` — lets this directory be added as a local marketplace
- `.mcp.json` — runs `sextant-mcp` (stdio MCP server, `cmd/sextant-mcp`)
- `hooks/hooks.json` — the `UserPromptSubmit` trust hook, `sextant-mcp attest`; the `Stop`/`SubagentStop` nudge hook
- `hooks/nudge.sh` — the Stop-nudge script (soft pre-stop reminder: subs, messages, review flags)
- `skills/sextant/SKILL.md` — conventions, topics/DMs/inboxes, verb selection, record shapes, identity setup
- `skills/startup/SKILL.md` — unattended-worker startup: connect, subscribe to the principal DM, handle inbound by trust level
- `skills/live-verify-v053/` — `/live-verify-v053`: prove the v0.5.3 runtimes are operational on the live setup (SKILL.md + `verify.sh`)
