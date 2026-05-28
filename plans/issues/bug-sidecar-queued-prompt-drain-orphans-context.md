---
title: Sidecar drains queued inbox on reconnect, producing flood of "orphan" responses with no visible originating prompts
status: open
priority: P2
created_at: 2026-05-26T17:35-07:00
labels: [bug, sidecar, nats, chat-tui, needs-input, operator-experience]
discovered_in: chat TUI Checkpoint C — after `agents restart` resolved a NATS disconnect, the chat filled with response frames from prompts the operator had sent during the disconnect window
---

## Deferred (2026-05-27)

This bug's open questions (drain semantics, operator visibility,
pairing with history) are downstream of the chat-vs-context
design decision. The context half landed via
[[feat-agents-context-view]]; the chat half is deferred (see
[[feat-chat-tui-history]]'s Deferred note). **Don't pick this up
until the chat surface gets its data-model conversation** — the
recommended fix shape (chat history captures user prompts at
submission time) only makes sense once chat history exists.

## Summary

When the sidecar's NATS connection drops (see [[bug-sidecar-nats-disconnect-no-reconnect]]) and the operator continues sending prompts via `sextant conversation` / `sextant ask`, the prompts queue **durably** on the agent's `inbox` JetStream subject. The daemon publishes successfully — the bus has the messages — but the sidecar isn't subscribed so nothing is processed.

When the sidecar comes back (`agents restart`, manual reconnect, etc.), it drains the entire inbox backlog at once. Each queued prompt produces response frames, all timestamped at the drain time rather than the original prompt time. From the operator's perspective in the chat TUI:

- A flood of agent responses appears, all very close in time
- The operator's own prompts that triggered them aren't visible (those were emitted earlier; the chat default subscribes at "now" with no history — see [[feat-chat-tui-history]])
- Each response feels orphaned: a turn answering a question the operator can't see

Repro from the discovery session: docker logs showed ~20 `inbox: prompt queued` events accumulating during the disconnect window (22:49Z–23:18Z); after restart at 00:30Z, the sidecar produced response frames for each in quick succession.

## Why this is murky

The current behavior **is** correct in the "no data loss" sense: prompts queue durably, get processed when the sidecar returns, no input is silently dropped. That's a strong invariant worth preserving.

But the operator UX is bad. The desired behavior is genuinely unclear and worth thinking about as a system design question, not just a bug fix:

- **Drain everything (current).** Strong durability, confusing UX without history.
- **Drain only the latest.** Closer to "user expected to start fresh" — but throws away their queued work.
- **Surface a "N queued prompts replaying" banner in the chat.** Keeps durability, adds context.
- **Make the chat TUI's history feature** ([[feat-chat-tui-history]]) **include the queued user prompts** so responses have their originating prompts on the same screen. Fixes the orphan feel without changing sidecar behavior.
- **Drop the backlog when the sidecar reconnects after > N minutes of disconnect**, since the operator probably moved on.

These choices interact with the chat-vs-context split Lena is planning (per [[feat-chat-tui-history]] open questions). The right answer probably emerges from that design conversation, not from a narrow fix here.

## Needs Lena's input

Specifics deferred to the broader chat / message-system design pass. This ticket exists to track the observation and the trade-off, not to prescribe.

Open questions:

1. **Drain semantics.** All / latest / none / time-bounded?
2. **Operator visibility.** Should the chat TUI surface the drain explicitly (banner, "playing back N queued prompts")?
3. **Pairing with history.** Does the chat history feature ([[feat-chat-tui-history]]) need to capture queued-but-unprocessed prompts as user turns at their submission time, so responses have visible context?
4. **Interaction with `[[bug-sidecar-nats-disconnect-no-reconnect]]`.** If reconnect is fast (seconds), backlog is small and drain is fine. If reconnect is slow (laptop slept overnight), backlog could be huge. Does the answer differ by backlog age / size?

## Related

- `[[bug-sidecar-nats-disconnect-no-reconnect]]` — the precondition. Fix that and backlogs become rare and small.
- `[[feat-chat-tui-history]]` — the place this gets resolved. If history captures user prompts at submission time, the orphan-response problem disappears.
- `[[bug-prompt-agent-accepts-when-sidecar-gone]]` — adjacent: should `prompt_agent` refuse during a known disconnect, so prompts don't queue in the first place?
