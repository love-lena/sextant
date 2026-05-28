---
title: Show the agent name and lifecycle status explicitly in the chat TUI header
status: open
priority: P3
created_at: 2026-05-27T19:48-07:00
labels: [feature, tui, chat, polish, observability]
discovered_in: post-lifecycle-truth smoke testing — the header today carries a color-coded dot whose semantics ("destructive" red could mean ended, crashed, OR lost) are invisible to anyone who hasn't memorized the mapping; the agent name is shown but the lifecycle state itself is not
---

## Summary

The chat TUI header today renders:

```
●  assistant  ⎇ main
────────────────────
```

The dot is color-coded by lifecycle transition (success / attention /
destructive / lost / muted — see `pkg/tui/chat/standalone.go::lifecycleDotRoleClass`),
but the **actual state word is never shown**. An operator looking at the
TUI cannot tell `ended` from `crashed` from `lost`; they all render as
a red dot. After the lifecycle-truth landing this is more
load-bearing — `lost` and `crashed` are now distinct concepts the
operator needs to act on differently.

Agent name is already shown via `m.opts.AgentName`, but it's small and
sits next to a glyph that may be the only header signal an operator
notices.

## Fix shape

Render the lifecycle word inline, next to the dot. Examples:

```
●  assistant · running           ⎇ main
●  assistant · lost              ⎇ main       (red, banner below: "press R to restart")
●  assistant · ended (12m ago)   ⎇ main       (muted)
```

Specifics:

1. `pkg/tui/chat/standalone.go::renderHeader` — append `" · " + state`
   after the name, where `state` derives from `m.lastLifecycle.State`
   (or `Transition` if State isn't set — see how the dot color picker
   handles the empty case).
2. Use the same role-class palette already in `lifecycleDotRoleClass`
   so the word color matches the dot.
3. Show a relative timestamp on terminal states (`ended (12m ago)`,
   `lost (just now)`). Pull from `m.lastLifecycle.Ts` if it exists, or
   from the time the envelope arrived.
4. Update the standalone test that pins the dot mapping
   (`TestRenderLifecycleDotSelectsRoleByTransition`) to also assert the
   word appears in the rendered output.

## Why now

The lifecycle-truth PR shipped a fourth state (`lost`) on top of the
existing three terminal states (`ended` / `crashed` / `archived`). With
four reds-or-yellows that all render as a single colored dot, operator
diagnosis from the TUI requires guessing or running `sextant agents
check` in a second pane. Making the state word visible is cheap and
closes the gap.

## Related

- [[feat-chat-tui-status-dot]] — the original dot work; this is its
  natural follow-up.
- [[feat-tui-chat-restart-error-banner]] — also lives in the header /
  banner area; coordinate placement if these land together.
