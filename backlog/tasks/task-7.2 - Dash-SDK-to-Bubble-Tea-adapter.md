---
id: TASK-7.2
title: 'Dash: SDK to Bubble Tea adapter'
status: To Do
assignee: []
created_date: '2026-06-06 02:59'
labels: []
milestone: 'M4: The dash (human UI)'
dependencies:
  - TASK-3
  - TASK-4
references:
  - docs/adr/0023-the-dash-is-a-composable-pane-cockpit.md
  - docs/adr/0014-the-tui-is-a-client.md
parent_task_id: TASK-7
priority: medium
ordinal: 31000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The thin tea.Cmd adapter that bridges an SDK subscription into the Bubble Tea loop by re-yielding bus events as tea.Msg (ADR-0023; replaces the old Source/Pump, which stays collapsed into the SDK per ADR-0014). Round-trip merge: self-published messages return on the same subscription, no optimistic echo.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 a tea.Cmd subscribes via the SDK and re-yields each event as a tea.Msg
- [ ] #2 round-trip merge: a sent message arrives via the same subscription (no optimistic echo)
- [ ] #3 public SDK only, no bus/NATS types leak into the TUI; teardown cancels cleanly
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
**Brief (resolved design — self-contained).**

Package `pkg/tui/busfeed`. Imports `pkg/sextant` + bubbletea only — **no `nats.go`, no
`internal/`** (import check is part of done).

Wraps the public SDK (`pkg/sextant/messages.go`): `Subscribe(ctx, subject, Handler,
opts...) (Subscription, error)` is **push/callback**; frames arrive already
validated + epoch/skew-checked as `sextant.Message{Frame, Subject, BusTime, Sequence}`.
`DeliverAll()` replays backlog; `Publish(ctx, subject, json.RawMessage)`;
`FetchMessages(...)` is the pull complement.

Design — the canonical Bubble Tea external-stream bridge: the `Handler` does a
**non-blocking** send onto a buffered channel (cap ~256); a re-issued `Next()` `tea.Cmd`
reads it and returns a typed `EventMsg{sextant.Message}`; on `EventMsg`, Update returns
`Next()` again. Teardown: `Stop()` / ctx-cancel → `Subscription.Stop()`.

Locked decisions:
- Overflow is **fail-loud**: drop on a full buffer, count, surface a coalesced
  `DroppedMsg{N}` (UI shows a gap marker) — no silent loss; no ring buffer yet.
- **Round-trip merge = do nothing special** (ADR-0023): a self-`Publish` returns on the
  same subscription; *visible == durably on the bus*; **no optimistic echo**. The
  message-stream surface (7.3) subscribes to the subject it publishes to.
- Live-only by default; `DeliverAll` is passthrough; history via `FetchMessages` is out
  of scope here. Errors surface as an `ErrMsg`; no hidden reconnect.

Verify (no PTY — no render): integration test on an embedded/real bus — subscribe →
publish → receive the same frame as `EventMsg`; `DeliverAll` gives backlog→live; `Stop`
is goleak-clean; overflow fires `DroppedMsg`. Import lint proves public-SDK-only.
<!-- SECTION:NOTES:END -->
