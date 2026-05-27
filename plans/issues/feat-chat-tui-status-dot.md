---
title: Chat TUI header should show a lifecycle status dot
status: open
priority: P2
created_at: 2026-05-26T15:05-07:00
labels: [feature, tui, chat, operator-experience]
discovered_in: chat TUI Checkpoint C — operator opened the chat for a dead agent and got no signal; original spec listed this as a header element that we deferred to post-MVP
---

## Summary

The original chat TUI spec (`plans/issues/feat-chat-tui.md` §"Header") called for:

> Status dot (pulses when the agent has pending lifecycle attention).

This was deferred in the MVP for scope reasons. Bringing it forward solves a class of operator-experience problems that the Checkpoint C debug session exposed: the operator opened the chat for an agent that was actually ended hours earlier and had no visual indication that talking to it was futile. They sent prompts, watched their local echoes appear, and waited for responses that never came.

A static colored dot reflecting the most recent lifecycle envelope on the chat's subscription stream — green for `running` / `turn_ended`, yellow for `paused` / `archived`, red for `ended` / `crashed` — would surface that state instantly.

Target header line:

```
●  alice  ⎇ main                                                                                    [lifecycle=ended since 14:12:32]
```

Or, more minimally:

```
●  alice  ⎇ main
```

with the dot's color carrying the entire signal.

## Why P2

Doesn't fix the root cause (`[[bug-agents-list-stale-lifecycle]]`) but is the cheapest, most-visible operator signal we can give. The chat TUI is now the default surface for `sextant conversation` — every operator-agent interaction goes through it. Surfacing lifecycle in the header pays off on every session.

## Implementation shape

The chat package already subscribes to lifecycle envelopes (`lifecycleMsg` handler in `pkg/tui/chat/model.go` Update). Currently the handler is a no-op (`_ = msg`). Change it to:

1. Store the most recent `LifecyclePayload` on the `Model` (`lastLifecycle sextantproto.LifecyclePayload`).
2. In `renderHeader`, before the agent name, render a dot styled by the transition:
   - `transition=started`, `turn_ended`, `running` → `colSuccess` (`green`)
   - `transition=paused`, `archived` → `colAttention` (`yellow`)
   - `transition=ended`, `crashed` → `colDestructive` (`red`)
   - Unknown / no lifecycle yet → `colMuted`
3. Use `lipgloss.AdaptiveColor` for terminal-theme portability (existing pattern).

Add a small role token `LifecycleDot` to `style.go` for each variant — wrapper functions `dotStyle(transition)` are fine if we want one token table rather than four.

Optionally surface a more detailed badge on the right side of the header for non-healthy states ("ended 14h ago — `R` to restart") and bind `R` to a new `restart_agent` RPC dispatch — but that's its own feature; for this issue just the dot.

## Acceptance

- `TestChatHeaderDotReflectsLifecycle` — boot the model, dispatch a `lifecycleMsg{Transition: ended}`, assert the rendered header contains the destructive-styled dot glyph.
- Same for `paused` (attention) and `turn_ended` (success).
- Initial state (no lifecycle envelope yet) shows the muted dot.

## Related

- `plans/issues/feat-chat-tui.md` §"Header" §"Deferred" — original spec entry.
- `[[bug-agents-list-stale-lifecycle]]` — root cause that this mitigates.
- `[[bug-prompt-agent-accepts-when-sidecar-gone]]` — alternative root-cause fix; this dot is a complementary UI hint.
