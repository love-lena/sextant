# sextant — the Claude Code plugin

Makes a Claude Code session a first-class sextant client (ADR-0028): the bus
verbs as MCP tools under one verified identity, inbound messages pushed into
the session as channel events, and a skill teaching the conventions.

## Demo

```bash
clients/claude-code/demo.sh
```

Throwaway bus, two identities, a CLI peer that auto-replies, and a live
session with channel push on. Follow the three printed steps; exit for the
bus-side transcript.

## Install

The [root quickstart](../../README.md#quickstart) has the setup: binaries on
PATH, a reachable bus (`sextant up`), then the marketplace add + install +
register. The GitHub add clones with your git credential helper, so `gh` auth
covers the private repo. Offline, or from an unpacked release tarball, add
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
- `skills/sextant/SKILL.md` — conventions, verb selection, record shapes, identity setup
