# sextant — the Claude Code plugin

Makes a Claude Code session a first-class sextant client (ADR-0028): the bus
verbs as MCP tools under one verified identity, inbound messages pushed into
the session as channel events, a skill teaching the conventions, and an
auth/signing hook (ADR-0030) that stamps inbound messages with their verified
author and a trust level.

## The trust hook (ADR-0030)

A `UserPromptSubmit` hook (`hooks/hooks.json` → `sextant-mcp attest`) runs on
each woken turn. It reads new inbound messages on this session's own DM subject,
stamps each by its unforgeable bus-stamped **author ULID** with a trust level —
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

## Layout

- `.claude-plugin/plugin.json` — the plugin manifest
- `.claude-plugin/marketplace.json` — lets this directory be added as a local marketplace
- `.mcp.json` — runs `sextant-mcp` (stdio MCP server, `cmd/sextant-mcp`)
- `hooks/hooks.json` — the `UserPromptSubmit` trust hook, `sextant-mcp attest`
- `skills/sextant/SKILL.md` — conventions, verb selection, record shapes, identity setup
