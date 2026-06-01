---
id: TASK-82
title: pkg/tui/pending Component for `sextant pending list -i`
status: Done
assignee: []
created_date: '2026-05-28 16:00'
labels:
  - feature
  - tui
  - cli
  - 'slug:feat-tui-pending-component'
  - P3
  - 'closed:fixed'
dependencies: []
priority: low
ordinal: 82000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Summary

`sextant pending list -i` was wired during
[[feat-cli-iflag-tier1-components]] but the underlying
`pkg/tui/pending` Component does not yet exist. The flag is
discoverable in `--help` and surfaces a clear error pointing here:

```
$ sextant pending list -i
sextant: pending list -i: pkg/tui/pending Component not yet
implemented; see plans/issues/feat-tui-pending-component.md
```

The static `pending list` path is unaffected. Today's pending
list is a non-interactive RPC snapshot driven by a 500ms quiet-
timer drain over `user_input.>` — it doesn't have a Component
shape yet.

## Shape

Build `pkg/tui/pending.Model` with the Component contract from
`pkg/tui/component`:

- Lists unanswered user_input requests, refreshed on
  `user_input.requests.>` envelopes.
- Single-row navigation (j/k), Enter opens an answer pane (out of
  scope for v1; just publish a structured intent via component
  `OpenMsg`).
- Quit via q → emits `component.DoneMsg`.
- `init()` registers with `pkg/tui/component`'s registry.

Then drop the placeholder branch in
`cmd/sextant/iflag.go::addPendingListIFlagFollowUp` and wire the
real launcher analogous to `runAgentsListTUI`.

## Acceptance

- `sextant pending list -i` opens the TUI and exits cleanly on `q`.
- Component registration shows up in `component.List()`.
- Smoke test in cmd/sextant proves the launcher is invoked.

## Related

- [[feat-cli-iflag-tier1-components]] — parent (resolved).
- [[feat-tui-component-interface]] — Component contract.
- [[feat-sextant-tui-discovery]] — consumes the registry.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-tui-pending-component.md
Discovered in: 2026-05-28 implementation of [[feat-cli-iflag-tier1-components]]
Original created_at: 2026-05-28T16:00-07:00
Fixed in: 835b7af
<!-- SECTION:NOTES:END -->
