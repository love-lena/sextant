---
title: pkg/tui/traces Component for `sextant traces show <id> -i`
status: open
priority: P3
created_at: 2026-05-28T16:00-07:00
labels: [feature, tui, cli, observability]
discovered_in: 2026-05-28 implementation of [[feat-cli-iflag-tier1-components]]
---

## Summary

`sextant traces show <trace_id> -i` was wired during
[[feat-cli-iflag-tier1-components]] but the underlying
`pkg/tui/traces` Component does not yet exist. The flag is
discoverable in `--help` and surfaces a clear error pointing
here:

```
$ sextant traces show abc-123 -i
sextant: traces show -i: pkg/tui/traces Component not yet
implemented; see plans/issues/feat-tui-traces-component.md
```

The static `traces show` renders a span tree to stdout via
`renderSpanTree` in `cmd/sextant/traces.go` — that path is
unchanged.

## Shape

Build `pkg/tui/traces.Model` with the Component contract from
`pkg/tui/component`:

- Renders the span tree as an interactive collapse-expandable
  outline. Today's renderSpanTree builds the same tree structure
  for stdout; lift it into the Component so the layout logic is
  shared.
- j/k navigate between spans; Enter expands the focused span
  (showing attributes / events); Esc collapses.
- Quit via q → emits `component.DoneMsg`.
- LoadMsg{ID: trace_id} (re-)fetches the trace via the
  `query_trace` RPC.
- `init()` registers with `pkg/tui/component`'s registry.

Then drop the placeholder branch in
`cmd/sextant/iflag.go::addTracesShowIFlagFollowUp` and wire the
real launcher.

## Acceptance

- `sextant traces show <id> -i` opens the TUI and exits cleanly
  on `q`.
- Component registration shows up in `component.List()`.
- Smoke test in cmd/sextant proves the launcher is invoked.

## Related

- [[feat-cli-iflag-tier1-components]] — parent (resolved).
- [[feat-tui-component-interface]] — Component contract.
- [[feat-sextant-tui-discovery]] — consumes the registry.
