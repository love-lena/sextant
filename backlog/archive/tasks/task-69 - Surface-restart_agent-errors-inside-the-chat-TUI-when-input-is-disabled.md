---
id: TASK-69
title: Surface restart_agent errors inside the chat TUI when input is disabled
status: Done
assignee: []
created_date: '2026-05-27 18:55'
labels:
  - feature
  - tui
  - chat
  - polish
  - follow-up
  - 'slug:feat-tui-chat-restart-error-banner'
  - P3
  - 'closed:fixed'
dependencies: []
priority: low
ordinal: 69000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Summary

The lost-state TUI affordance shipped with `feat-lifecycle-truth` keeps
input disabled until the new incarnation publishes `started`. If the
`restart_agent` RPC itself fails before that signal can arrive, the
operator sees nothing — the banner doesn't update and R becomes the
only way out, but they don't know to press it again.

Today the error is logged via `log.Printf` (`pkg/tui/chat/program.go`'s
`makeRestartHook`), which is fine for daemon-log forensics but invisible
to the operator inside the TUI.

## Fix shape

1. Add an inline-banner state to `Model` (e.g. `lastError string`,
   cleared on next lifecycleMsg or after N seconds).
2. Emit a `restartFailedMsg{Err string}` from `makeRestartHook` when the
   RPC errors; `Standalone.Update` (or the model's Update) consumes it
   and sets the banner.
3. `standalone.go::renderHeader` (or wherever the lost-state banner
   lives) prepends the error.
4. Pressing R again clears the banner and retries.

## Why P3

The structural fix (lifecycle truth) is shipped and correct. This is a
polish item — restart_agent rarely fails on a healthy host, and when it
does the daemon log captures the cause. Operators with TUI-only access
should still see the error inline; until they do, the workaround is
`sextant daemon logs --tail 20`.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-tui-chat-restart-error-banner.md
Discovered in: Codex review of feat/lifecycle-truth-2026-05-27 — when an agent is `lost`, the chat TUI disables input and binds R to restart_agent; if restart_agent fails (daemon unreachable, container manager down, etc.) the error is logged but the operator's prompt area stays locked with no visible feedback
Original created_at: 2026-05-27T18:55-07:00
Fixed in: 3532a34
<!-- SECTION:NOTES:END -->
