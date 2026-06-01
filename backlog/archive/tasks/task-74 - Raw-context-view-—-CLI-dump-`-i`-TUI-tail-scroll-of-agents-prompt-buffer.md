---
id: TASK-74
title: Raw-context view — CLI dump + `-i` TUI tail/scroll of agent's prompt buffer
status: Done
assignee: []
created_date: '2026-05-27 20:22'
labels:
  - feature
  - cli
  - tui
  - context
  - observability
  - sidecar
  - 'slug:feat-agents-context-view'
  - P2
  - 'closed:resolved'
dependencies: []
priority: medium
ordinal: 74000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Resolution

Both phases shipped.

- **Phase A** (CLI dump): `sextant agents context <agent>` + `--follow`
  + `--mode=` filters, on `pkg/sessionlog` — PR #28 (`8e8f26b`), v0.3.0.
- **Phase B** (`-i` TUI): the tailing `StreamViewport` with mode keys
  1–6, `pkg/tui/contextview` — PR #39 (`3bae7ee`), v0.4.0. The per-line
  renderers + `Mode`/`ParseMode` were lifted into `pkg/sessionlog`
  (`render.go`) so the CLI dump and the TUI render identically.

Built on the shared widget layer from the TUI workstream RFC
([`plans/rfc-tui-workstream.md`](../rfc-tui-workstream.md), P1). The
durable-replay + tool-definition-dump follow-ups noted in "Out of scope"
below remain unfiled P3 polish.

## Summary

A new operator surface for inspecting an agent's actual prompt
buffer / SDK session state in the rawest practical form. This is
the "context" half of the chat/context split; the chat half stays
deferred pending design (see [[feat-chat-tui-history]] and
[[bug-sidecar-queued-prompt-drain-orphans-context]]).

Two modes:

- **CLI dump.** `sextant agents context <agent>` prints the
  current context to stdout once, exits. Pipeable, scriptable.
- **`-i` TUI.** `sextant agents context <agent> -i` opens a
  scrollable pager that shows the current context AND tails it.
  Multiple view modes (raw, conversation, tool activity, thinking,
  usage, subagent tree) layered on the same event stream.

The semantic guarantee is "the rawest thing the sidecar can
serialize" — no projection, no curation. Raw mode is the floor;
the typed view modes are filters on top of that floor, not
substitutes for it.

## Why "rawest" matters

When something goes wrong with an agent — bad response, weird
loop, surprising tool call — the operator's question is "what did
the agent actually have when it decided to do that?" Today the
answer requires restarting the agent or guessing. A raw-context
surface lets the operator see exactly the inputs the SDK was
working with.

This is the observability complement to the lifecycle-truth work:
that gave us truth about agent *state*; this gives us truth about
agent *context*.

## Decisions (2026-05-27)

| Question | Decision |
|----------|----------|
| Mechanism | **File-tail the SDK session JSONL** — bind-mount the file out of the container; CLI/TUI reads/follows it. No new RPC, no new wire format. Operator can `tail -f` the raw file even when sextantd is down. |
| Payload | **Whatever the SDK persists** — the JSONL contains the message thread + content blocks (text / thinking / tool_use / tool_result) + per-turn usage stats + model/stop_reason. Tool definitions and sampling params are NOT in the file (they live in the sidecar wrapper); supplementary dump is a P3 follow-up if needed. |
| Verb | **`sextant agents context <agent>`** — new domain verb. Adds `context` to the closed-exception list in `conventions/tui-conventions.md`. The `-i` TUI mounts cleanly off this verb. |
| Retention | **Latest only** — bounded by what's still in the file (the SDK truncates / rotates on its own schedule). Durable replay via ClickHouse / JetStream is a P3 follow-up. |
| Resume-verb adjacency | **Deferred** — `feat-agents-resume-verb` stays a placeholder until pause ships. |

## Format reality (confirmed against a real Claude Code session)

Each line of the JSONL carries a discriminated record. The format
is the same one the CLI emits (the SDK persistence path uses the
same writer). Local inspection of a 313-line session:

```
  93  assistant       — model output (thinking | tool_use | text)
  55  user            — operator input (string) and tool_result returns
  49  queue-operation — CLI bookkeeping (probably absent in SDK mode)
  30  attachment      — file refs (CLI feature; sidecar likely doesn't produce)
  25  system          — system messages, local-command output
  16  worktree-state  — CLI bookkeeping
  16  mode            — CLI bookkeeping (normal/etc.)
  15  last-prompt     — CLI bookkeeping
  15  ai-title        — CLI bookkeeping
  10  file-history-snapshot — CLI bookkeeping
```

Assistant records carry more than text: `message.usage` (full
token breakdown with 5m/1h cache tiers), `message.model`,
`stop_reason`, `requestId`, `parentUuid`, `isSidechain`. That last
pair lets us reconstruct the subagent tree.

**Unknown:** the CLI bookkeeping records (mode, queue-operation,
ai-title, etc.) come from the *CLI's* writer path. The sidecar
uses `@anthropic-ai/claude-agent-sdk` programmatically, not the
CLI, so it may emit fewer noise records. The implementation pass
should verify by running a sidecar locally and counting record
types. The view-mode filters handle either case — noise records
collapse into "metadata" in conversation mode and disappear in
the typed modes.

## Implementation shape

Two-layer structure:

### Layer 1: `pkg/sessionlog` — typed JSONL parser

A small Go package that streams the session file as typed events.

- `func Stream(io.Reader) <-chan Event` — one event per line.
- `type Event interface { ... }` with concrete impls:
  `AssistantMessage`, `UserMessage`, `ToolUse`, `ToolResult`,
  `SystemMessage`, `Raw` (unknown / metadata types fall through).
- `AssistantMessage` exposes `Usage`, `Model`, `StopReason`,
  `ParentUUID`, `IsSidechain`, `ContentBlocks []Block` where
  `Block` is one of `TextBlock`, `ThinkingBlock`, `ToolUseBlock`,
  `ToolResultBlock`.
- No dependency on Bubble Tea or sextant internals — pure parsing
  + types. Reusable from the CLI dump path and the TUI alike.
- Tail-friendly: caller supplies an `io.Reader` that may block
  (e.g. `nxadm/tail`'s reader). The stream channel emits as data
  arrives.

### Layer 2: TUI view modes

The `-i` TUI mounts on top of `pkg/sessionlog`. Each view mode is
a separate render function over the same event stream.

| Key | Mode | Filter / transform | Operator question |
|-----|------|--------------------|-------------------|
| 1 | Raw | none (verbatim JSONL line) | "show me everything" |
| 2 | Conversation | role + text/tool blocks; hide metadata types | "what was said" |
| 3 | Tool activity | extract tool_use + matching tool_result; timeline | "what did the agent DO" |
| 4 | Thinking | only `thinking` content blocks from assistant records | "what was the agent REASONING about" |
| 5 | Usage | per-turn token totals + running cache-hit ratio (pulled from `message.usage`) | "is this agent expensive / cache-warm" |
| 6 | Subagent tree | group by `parentUuid` + `isSidechain` | "what dispatched what" |

Raw is the floor. Ship it first; other modes land incrementally.

### CLI shape

- `sextant agents context <agent>` — print current file to stdout, exit.
- `sextant agents context <agent> --follow` — `tail -f` semantics.
- `sextant agents context <agent> --mode=<raw|conversation|tools|thinking|usage|tree>` — filter the printed stream.
- `sextant agents context <agent> -i` — open the TUI. Mode keys 1–6 toggle.

### File-mount mechanics

The sidecar's `sessionId` is already persisted to the
agent_definitions KV (`images/sidecar/entrypoint/src/index.ts:374`).
The daemon needs to:

1. Resolve the agent's container's `~/.claude/projects/<encoded-cwd>/<sessionId>.jsonl` path.
2. Bind-mount that file (or its parent directory) to a host path
   under the daemon's runtime directory (e.g.
   `~/.local/share/sextant/agents/<uuid>/session.jsonl`).
3. Surface the host path in `AgentStatus` so the CLI can find it.

The exact mount mechanics need a short investigation during
implementation — Docker bind-mounts work cleanest at container-
creation time; we may need to point the container's
`~/.claude/projects/` at a per-agent host directory rather than
chasing the file post-spawn.

## Tooling landscape

- **Streaming JSONL in Go**: `encoding/json` over `bufio.Scanner`.
  No external deps needed for the parser itself.
- **File-watcher**: `github.com/nxadm/tail` (maintained `tail -f`
  with rotation handling) preferred over raw fsnotify for this
  shape. Append-only files map cleanly.
- **TUI**: Bubble Tea (already in use); `bubbles/viewport` for
  scroll; `bubbles/list` only if a particular view mode wants it.
- **No existing Claude-specific Go library** for this format that
  I know of. The TypeScript SDK exposes `SDKMessage` types; we
  mirror the relevant ones in `pkg/sessionlog`.
- **Document `jq` idioms** in the operator guide so even users
  who don't open the TUI can do `sextant agents context <id>
  --follow | jq 'select(.type=="assistant")'`.

## Acceptance

- `sextant agents context <agent>` prints the agent's current
  session JSONL to stdout and exits 0.
- `sextant agents context <agent> --follow` tails the file.
- `sextant agents context <agent> -i` opens the TUI in conversation
  mode (default). Keys 1–6 toggle view modes; raw mode shows the
  verbatim JSONL line; conversation mode shows role-styled blocks;
  usage mode shows running token totals.
- `pkg/sessionlog` is independently tested with a fixture JSONL
  exercising all record types.
- Smoke test: send a prompt via `sextant agents prompt`, observe
  the new turn land in the tailing context view within ~1s.
- `agents context` is added to the closed-exception list in
  `conventions/tui-conventions.md` with the "first-class operator
  concept" justification.

## Out of scope

- Tool-definition / sampling-param dump (P3 follow-up if needed).
- Durable replay across container restarts (P3 follow-up via
  ClickHouse).
- Filtering / searching across the file in the TUI (P3 polish).
- Chat surface — the chat-vs-context split is deferred pending
  design conversation.

## Related

- `[[feat-chat-tui-history]]` — the chat half of the chat/context
  split; deferred pending design.
- `[[bug-sidecar-queued-prompt-drain-orphans-context]]` —
  deferred; resolves separately once chat surface lands.
- `[[feat-agents-resume-verb]]` — deferred until pause ships.
- `[[feat-cli-i-flag-tier1-tier2]]` — the `-i` flag pattern this
  surface mounts onto.
- `[[feat-cli-verb-vocabulary-decision]]` — closed-exception list
  that `agents context` joins.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-agents-context-view.md
Discovered in: 2026-05-27 conversation deferring the chat-vs-context split — Lena's minimal context requirement was "see what the agent is working with in the rawest form possible, ideally tailing a file"
Original created_at: 2026-05-27T20:22-07:00
Resolved at: 2026-05-28T22:00-07:00
<!-- SECTION:NOTES:END -->
