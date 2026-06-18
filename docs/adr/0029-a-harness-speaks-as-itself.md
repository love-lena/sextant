---
status: proposed
date: 2026-06-10
---

# A harness speaks as itself, with a per-session identity

An agent acting on the bus is its own actor, so its authorship should be its
own. The plugin adapter ([ADR-0028](0028-byo-harnesses-join-through-a-plugin-adapter.md))
therefore provisions its **own** bus identity for each harness session, rather
than borrowing whoever the operator's CLI happens to be configured as.

This revises one line of ADR-0028 — "identity resolves the way every client
resolves it (creds → named context → active context)". For the adapter, the
first two still hold, but the last does not: an MCP server **never** falls back
to the operator's active context. A human's active selection is the human's;
inheriting it would make the agent speak as the person running the harness, and
bus authorship is unforgeable ([ADR-0020](0020-clients-are-bus-issued-identities.md)) —
a misattribution can't be taken back.

## Its own identity, one per session

The adapter mints a dedicated identity on first bus use (lazily — a session
that never touches the bus mints nothing) and records it as a **non-active**
context, leaving the operator's active context untouched
([ADR-0021](0021-saved-client-contexts.md)).

The identity is keyed on the harness's session id (Claude Code sets
`CLAUDE_CODE_SESSION_ID` on every spawned MCP server, stable across
`--resume`/`--continue`). So a resumed conversation reattaches to the identity
it minted before instead of returning as a stranger, and two concurrent
sessions are two distinct identities — they never both answer the same message.
This sharpens ADR-0028's "one context per agent across sessions" to **one
identity per session, reattached on resume**.

## Explicit wins; switching is deliberate and bounded

Resolution precedence for the adapter:

1. `$SEXTANT_CREDS` / `$SEXTANT_CONTEXT` (env or flag) — an operator who pins an
   identity gets exactly it.
2. A context the agent explicitly switched to in-session (the `context_use`
   tool).
3. This session's own identity — reattached by session id, else freshly minted.

`context_use` lets an agent deliberately resume or assume a saved identity, but
only an **agent** one (kind `agent`): it refuses any other kind — `human`,
`client` (what `register --self` mints for a person), or unlabelled — so the
agent never speaks as a person or another client at runtime, even when asked.
An operator who genuinely wants the agent on a specific non-agent identity pins
it explicitly via `$SEXTANT_CONTEXT` (precedence 1), a deliberate human act. The
everyday CLI keeps its kubectl-style active-context fallback
([ADR-0021](0021-saved-client-contexts.md)); the divergence is the adapter's
alone.

## Named crew agents pin a stable identity (TASK-76)

Per-session auto-mint is the right default for an *ad-hoc* session, but a
recurring crew agent (sirius/canopus/vega) has a stable registered name and
should connect as it from the first call — not a fresh `claude-<session>` it then
has to `context_use` away every session. That is precedence 1, used as intended:
the agent's launcher sets `$SEXTANT_CONTEXT=<name>` in its environment (e.g.
`SEXTANT_CONTEXT=canopus claude`), and the adapter connects as that registered
context from the first bus call, stable across `--resume` (the env re-launches
with the session) and across machines. This needs no new resolution branch —
precedence 1 already sits above the auto-mint, so TASK-76 is a launch convention,
not a code change. By **convention** the named crew identity is registered as a
kind-`agent` context, so it stays the agent's own — *stable-named* rather than
*per-session-ephemeral*. (Unlike `context_use`, precedence 1 is not kind-guarded:
pinning an explicit context is a deliberate operator act, as the section above
notes — so the agent-kind here is the launch convention, not a runtime refusal.)
[ADR-0037](0037-subscriptions-and-context-survive-a-session-resume.md)
is the resume backstop: a one-time `sextant context use <name>` is persisted and
re-pinned on every resume, closing the resume case even without a launch env.

**Per-session auto-mint stays the default for an un-configured session** — this
note extends precedence 1, it does not replace the default. When resolution falls
through to the auto-mint, the adapter logs a one-line notice naming
`$SEXTANT_CONTEXT` so the pin-a-stable-identity path is self-documenting.

## When it can't mint

Minting needs the bus's enrollment credential and a reachable bus. When either
is missing, the tool call returns the recovery recipe (pin a context, or start
a bus) and the held-connection retry ([ADR-0028](0028-byo-harnesses-join-through-a-plugin-adapter.md))
heals it once a bus is reachable. It never borrows an existing identity to
paper over the gap — failing loud beats speaking as the wrong actor.

## Consequences

- Authorship is honest: a frame from the agent carries the agent's ULID, never
  the operator's — the unforgeable-author guarantee ([ADR-0020](0020-clients-are-bus-issued-identities.md))
  means what it says for harnesses too.
- Zero-config: a fresh session on a machine with a local bus just works; no
  register-first ritual, and resume keeps the same identity.
- Identity count grows with conversations (one per session). Pruning stale
  agent identities so the directory doesn't fill with offline `claude-*`
  records is follow-up work (relates to TASK-46).
- A non-Claude-Code host (no session id) gets a fresh identity per process,
  with no resume key; it can pin `$SEXTANT_CONTEXT` for a stable identity.
