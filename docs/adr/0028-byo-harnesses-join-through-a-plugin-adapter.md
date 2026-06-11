---
status: proposed
date: 2026-06-10
---

# BYO harnesses join through a plugin adapter

An agent harness becomes a sextant client through a harness-native adapter,
not bespoke integration code. The reference adapter is the Claude Code plugin
(`clients/claude-code/` + `cmd/sextant-mcp`): an MCP server that exposes the
verb surface ([ADR-0017](0017-the-verb-surface-is-the-protocol.md)) as tools
and doubles as a *channel* — the Claude Code push mechanism that injects
inbound bus messages into the session. The adapter is a module over the locked
core ([ADR-0022](0022-modules-over-a-locked-core.md)): just another client
over the SDK, no special privilege
([ADR-0008](0008-clients-are-processes.md)).

## The adapter holds one identity for the session

One server process, one verified identity
([ADR-0012](0012-reserved-namespace-and-authn.md)), one connection held for
the server's lifetime — presence derives from that connection
([ADR-0020](0020-clients-are-bus-issued-identities.md)), so the session reads
as online to collaborators between tool calls, not only during them. Identity
resolves from explicit creds → named context, then — unlike the CLI — the
adapter provisions its *own* per-session identity rather than falling back to
the operator's active context ([ADR-0029](0029-a-harness-speaks-as-itself.md)).
Resolution is retried per tool call rather than failing the server, so the
adapter heals without a restart once a bus is reachable.

The **session is the client**: subagents inside the harness share its
identity, because the client boundary is the process and the bus does not
look inside it — signal and cooperate, never track and manage. Worker
attribution, where a workflow wants it, is record content. Each session gets
its own identity ([ADR-0029](0029-a-harness-speaks-as-itself.md)); a session
that genuinely needs two at once declares the adapter twice with different
contexts.

## Delivery maps by the verb's nature

One-shot and pull-batch verbs are tools; `message_read` is the portable
floor that works in any MCP harness. Push-stream verbs ride the harness's
push path: `message_subscribe` is a tool whose *delivery* is the channel,
opt-in per subject. A conformance test pins the mapping to
`protocol/methods.json` in both directions, with exclusions
(`clients.register`/`retire` are setup-time and CLI-owned; `artifact.watch`
is deferred) and extras (`message_unsubscribe`, channel control) declared
rather than implied.

Failure states are pushed, not swallowed: resume outcomes
([ADR-0027](0027-subscriptions-survive-a-bus-restart.md)) arrive as system
notices (`resume_deferred`, `resume_lost`), and because the harness drops
channel events silently when the channel isn't enabled, every subscribe is
answered with a delivery caveat and followed by a `subscribed` notice the
agent can check.

## Consequences

- Any MCP-speaking harness gets the tool surface for free; only the push
  path is Claude Code-specific (research preview, allowlist-gated — the dev
  flag for own use, Anthropic allowlisting for distribution).
- Other harnesses (TASK-5's TypeScript SDK consumers, Mastra) follow the
  same shape: adapter as a module, verbs as the surface, harness-native
  push where one exists, `message.read` where one doesn't.
- The skill, not the protocol, carries the conventions (record shapes, verb
  selection, identity setup) — primitives stay policy-free.
