---
title: Wire -i flag for Tier 1 component TUIs + add init()-time registry
status: resolved
priority: P3
created_at: 2026-05-28T10:54-07:00
resolved_at: 2026-05-28T13:56-07:00
labels: [feature, cli, tui, architecture]
discovered_in: 2026-05-28 split of feat-cli-i-flag-tier1-tier2 after architecture decisions baked in
---

## Resolution

Shipped via PR #27 (`12e01f2`), target main. Both pieces landed:
the `init()`-time registry (`pkg/tui/component/registry.go` —
`Register` / `List`, with a double-register panic guard) and the
`-i` / `--tui` flag on Tier 1 commands. `agents list -i` and
`agents show <id> -i` mount the existing agents TUI inline; each
component package self-registers via `init()`.

`pending list -i` and `traces show <id> -i` accept the flag but
surface a clear "not yet implemented" pointer — their Components
shipped as placeholders, tracked by follow-ups filed during the
work:

- [[feat-tui-pending-component]] — build the real `pkg/tui/pending`
  Component behind `pending list -i`.
- [[feat-tui-traces-component]] — build the real `pkg/tui/traces`
  Component behind `traces show <id> -i`.
- [[feat-agents-context-view]] Phase B — `agents context <agent> -i`
  mounts on this registry once its TUI view modes land.

## Summary

The `Component` interface + `Host` wrapper landed via
[[feat-tui-component-interface]] (resolved 2026-05-26). What's
missing is two pieces:

1. An `init()`-time registry inside `pkg/tui/component/` that each
   component package self-registers with.
2. The `--tui` / `-i` flag on Tier 1 cobra commands that mounts
   the registered component via the existing `Host` helper.

Once both exist, [[feat-sextant-tui-discovery]] and
[[feat-sextant-dash-multipane]] can both walk the registry.

## Shape

### 1. Registry

Add `pkg/tui/component/registry.go`:

```go
// Meta describes a registered Tier 1 component for discovery surfaces.
type Meta struct {
    Name        string // short identifier ("agents-list", "chat", "pending-list")
    Description string // one-line summary for the Huh menu
    Command     string // cobra command path to invoke (e.g. "agents list")
    New         func() Component // factory the host uses to mount the component
}

// Register adds a component to the global registry. Intended to be
// called from each component package's init().
func Register(m Meta) { ... }

// List returns all registered components in registration order.
func List() []Meta { ... }
```

Each Tier 1 component package then has:

```go
// pkg/tui/agents/agents.go
func init() {
    component.Register(component.Meta{
        Name:        "agents-list",
        Description: "Browse and manage running agents",
        Command:     "agents list",
        New:         func() component.Component { return New() },
    })
}
```

### 2. `--tui` / `-i` flag on Tier 1 commands

Wire on:

- `sextant agents list -i` → mount `pkg/tui/agents.Model`
- `sextant agents show <id> -i` → mount the same, focused on `<id>`
- `sextant agents context <agent> -i` → mount the
  `pkg/tui/context.Model` once it exists (see
  [[feat-agents-context-view]])
- `sextant pending list -i` → mount `pkg/tui/pending.Model`
- `sextant traces show <trace_id> -i` → mount the trace explorer

Each `-i` handler hosts the component via the existing `Host`
wrapper. The positional arg (e.g. `<id>`) seeds a `LoadMsg` into
`Init`.

### 3. Tests

- `pkg/tui/component/registry_test.go` — registration adds entries;
  `List()` returns them in order; double-register on the same
  Name panics (catches accidental duplicate registrations at boot).
- One smoke test per `-i` flag that asserts the cobra command
  accepts the flag and mounts the component (use a fake Host that
  records the mount).

## Acceptance

- `component.Register` + `component.List` exist and are documented.
- Existing components (`pkg/tui/chat/`, `pkg/tui/agents/`) call
  `Register` in their package `init()`.
- `sextant agents list -i` opens the interactive agents list and
  exits cleanly on `q`.
- `sextant agents show <id> -i` mounts the same with `<id>` seeded.
- `sextant pending list -i` opens the pending TUI.
- `sextant traces show <id> -i` opens the trace explorer.
- Tests cover registration semantics + the `-i` plumbing.

## Why P3

Foundation work. Doesn't change operator-visible behavior of
existing commands; just adds the interactive variants. P3 because
operators today use the standalone TUI binaries
(`cmd/sextant-tui-agents/`, `cmd/sextant-tui-chat-preview/`) for
the same surfaces; this consolidates the entry path.

## Out of scope

- `sextant tui` discovery menu — [[feat-sextant-tui-discovery]].
- `sextant dash` multipane TUI — [[feat-sextant-dash-multipane]].
- Mouse support beyond what `Host` already provides — covered in
  the dash ticket where BubbleZone lands.

## Related

- [[feat-tui-component-interface]] — the interface this builds on
  (resolved).
- [[feat-sextant-tui-discovery]] — sibling.
- [[feat-sextant-dash-multipane]] — sibling.
- [[feat-cli-i-flag-tier1-tier2]] — original umbrella ticket
  (resolved by this split).
- [[feat-agents-context-view]] — adds `agents context -i` to the
  list above when it ships.
