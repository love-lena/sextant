---
id: TASK-39
title: 'Chat TUI doesn''t replay history on reopen — feature gap, open design questions'
status: To Do
assignee: []
created_date: '2026-05-26 15:57'
labels:
  - feature
  - tui
  - chat
  - data-model
  - needs-input
  - 'slug:feat-chat-tui-history'
  - P2
dependencies: []
priority: medium
ordinal: 39000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Deferred (2026-05-27)

The chat-vs-context split was discussed and the **context half**
shipped via [[feat-agents-context-view]]. The chat half — including
this ticket's open questions about history source-of-truth, replay
scope, and "chat" vs "context" surface boundaries — is **deferred
pending Lena's broader chat-design conversation**. Don't pick this
up without that conversation; the open questions below are real
and the wrong answer locks the chat surface into the wrong data
model. Ship the context surface first; chat follows.

## Summary

`sextant conversation <agent>` currently subscribes to the agent's `frames` + `lifecycle` subjects starting at "now". On reopen, no prior turns appear — the operator sees an empty stream until fresh activity arrives. This is a real feature gap: chat history clearly exists somewhere (the events are on JetStream, persisted in ClickHouse), but the chat doesn't show it.

The infrastructure for a seed is half-built — `pkg/tui/chat.RunConfig` has a `SeedTurns []Turn` field designed exactly for this — but nothing populates it on the default open. `--from-seq N` exists for manual resume but requires the operator to already know a sequence number.

## Why this isn't a quick fix

There's a deeper data-model question worth resolving before picking an implementation:

**Where should the chat draw its history from?**

- **(A) Replayed events** (ClickHouse / JetStream): the easiest path, reuses `pkg/client.Query` against `agents.<uuid>.frames`. But it shows the chat as a *projection of events* — which can drift from the agent's actual conversation context (e.g. if some frames were dropped, the agent's internal state may include things the chat won't show, and vice versa).
- **(B) The agent's actual conversation context** (a direct query to the agent / its session store): zero drift by construction. The chat shows exactly what the agent thinks the conversation is. Requires an RPC against the agent (or a sidecar endpoint) that doesn't yet exist.
- **(C) Something else**: a curated "chat view" computed daemon-side, intentionally different from both raw events and the agent's context.

This question intersects with a planned iteration on the chat interface: splitting **"chat"** (the operator's conversational view) from **"context"** (what the agent actually has in its prompt buffer). If those become two separate surfaces, the history strategy may differ per surface.

## What this ticket is

A placeholder so the gap is tracked, not a prescription. Specifics deferred to a design conversation with Lena.

## Open questions (needs Lena's input)

1. **Source of truth.** A vs B vs C above. Implications for drift, latency, and which RPCs need to exist.
2. **How "chat" and "context" split.** If they're separate surfaces, does each have its own history story, or is one a filter of the other?
3. **Replay scope.** Last N turns, last X minutes, full history, lazy-load on scroll? Different choices interact with the source-of-truth question.
4. **Lifecycle replay.** Should the seed include lifecycle envelopes (for [[feat-chat-tui-status-dot]] + the status bar's live indicator), or is that a separate concern?
5. **Behavior on long histories.** A chatty agent could have thousands of frames. Cap at the storage layer? At the TUI layer? Render lazily? Drop oldest from memory?

## Related

- `plans/issues/feat-chat-tui.md` §"Implementation shape" — mentions `--from-seq` as preserved but doesn't auto-seed.
- `pkg/tui/chat/program.go` — `RunConfig.SeedTurns` is wired up and ready to receive a backfill; nothing populates it yet.
- `[[feat-chat-tui-status-dot]]` — wants the latest lifecycle envelope at open; overlaps with the seed query.
- `[[bug-agents-list-stale-lifecycle]]` — same general theme: daemon's view of agent state should reflect what's on the bus.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-chat-tui-history.md
Discovered in: chat TUI Checkpoint C — operator closed chat, reopened, got an empty stream until the agent emitted a new frame
Original created_at: 2026-05-26T15:57-07:00
<!-- SECTION:NOTES:END -->
