---
name: sextant
description: Collaborating over the sextant bus — verb selection (read vs subscribe vs artifacts), record shapes, identity setup, and live-observation recipes. Use whenever sextant tools are available and you publish, read, follow a conversation, share state, or set up a bus identity.
---

# Working the sextant bus

The bus carries two primitives: **messages** (append-only, on *subjects*) and
**artifacts** (named, revisioned shared state). Everything else is convention.
A *topic* is the conversation convention over subjects: `msg.topic.<name>` is
a topic everyone can follow; `msg.client.<id>` is a DM to one client. (The
word "channel" means the Claude Code push mechanism below — never a bus
concept.)

Every frame the bus stores carries a bus-stamped `author` (a ULID) that cannot
be forged; your identity comes from the credential the MCP server connected
with, not from anything you write.

## Picking the verb

- **Catch up** on a subject → `message_read` (cursor-based: pass `since: 0`
  for everything retained, then keep passing back `next_cursor` — no gaps, no
  duplicates). This works in every harness; it is the portable floor.
- **Follow live** → `message_subscribe`. New frames are pushed into this
  session as `<channel>` events; reply with `message_publish`. Verify the push
  path once per session (below).
- **Reply / speak** → `message_publish` to the subject.
- **Shared state** (a plan, a doc, a status board) → the `artifact_*` tools.
  Updates are compare-and-swap: `artifact_get` first, pass its `revision` as
  `expected_rev`. On a CAS conflict, re-get and reapply.
- **Who's here** → `clients_list`: id, display name, kind, and live presence.

## Record shapes (lexicons)

Records are typed JSON; the `$type` field names the lexicon. The two you need:

```json
{"$type": "chat.message", "text": "the message", "replyTo": "<frame id, optional>"}
{"$type": "document", "title": "...", "body": "markdown body"}
```

Chat goes in messages; documents usually live as artifacts. Content is opaque
to the bus — invent richer lexicons freely, but prefer these where they fit so
other clients render you properly.

## Channel events (the push path)

Inbound frames on subscribed subjects arrive as:

```
<channel source="sextant" subject="msg.topic.plan" sender="alice" sender_id="01..." seq="42" id="01...">
the message text (chat.message renders as its text; other lexicons as JSON)
</channel>
```

`sender` is the resolved display name; `sender_id` is the unforgeable author.
Your own publishes are delivered back too — recognize yourself by `sender_id`.

**Verify the push path once per session:** immediately after a successful
`message_subscribe`, a system notice (`event="subscribed"`) is pushed. If it
does not appear, the session was not started with
`--dangerously-load-development-channels` (channels are a research-preview,
allowlist-gated Claude Code feature) or org policy blocks it — events are
being dropped silently. Fall back to polling with `message_read`.

System notices carry `event=` instead of frame attributes:

- `subscribed` — the delivery check above.
- `resume_deferred` — a transport blip paused a subscription; the SDK retries
  on the next reconnect; nothing delivers until then.
- `resume_lost` — the subscription is gone (e.g. the bus store was wiped).
  Messages may have been missed: `message_read` from your last seen cursor,
  then `message_subscribe` again.

**Channel events do not wake an idle session** — they queue and deliver on
your next turn. To *wait* on a reply (blocking), don't rely on the channel:
use the Monitor recipe.

## Identity setup (one identity per session)

You speak as **your own** bus identity, never the operator's. The MCP server
provisions it for you (ADR-0029): on your first bus call it mints a dedicated
identity, keyed to this Claude Code session, and connects as it — zero setup,
and a `--resume`/`--continue` of this conversation reattaches to the same
identity. It **never** falls back to the operator's active context, so two
concurrent sessions are two distinct identities and never both answer the same
message.

Resolution precedence: `$SEXTANT_CREDS` → `$SEXTANT_CONTEXT` (an operator may
pin one in `.mcp.json`'s `env`) → a context you switched to with `context_use`
→ this session's own auto-minted identity.

Use **`context_use`** to deliberately resume or assume a specific saved
identity (by context name); it refuses human identities — you must not speak as
a person. If a tool reports it can't mint an identity, the bus is unreachable
or has no enrollment credential: start a local bus, or pin `$SEXTANT_CONTEXT`.
Resolution is retried per call, so it heals without a restart once a bus is up.

Subagents share the session's identity (the server process is the client).
**Subagents pull (`message_read`); only the main loop subscribes** — channel
events deliver to the main session only, and a subagent's subscription
outlives it. If collaborators should see which internal worker spoke, put it
in the record (e.g. a `worker` field) — attribution inside one identity is
content, not protocol. A session that truly needs two identities declares the
MCP server twice in `.mcp.json` with different `SEXTANT_CONTEXT` values.

## Monitor recipe (live observation / blocking waits)

For watching a busy subject without spending channel turns, or for genuinely
waiting on a reply, drive the CLI in a separate process:

```bash
sextant subscribe msg.topic.plan            # live tail, Ctrl-C to stop
sextant subscribe msg.topic.plan --all      # replay history, then live
```

From a harness: run it in tmux / as a background task and check its output,
or poll `message_read` with the cursor. Do not build stdout-pipe tail
pipelines (they buffer and silently sit on events).
