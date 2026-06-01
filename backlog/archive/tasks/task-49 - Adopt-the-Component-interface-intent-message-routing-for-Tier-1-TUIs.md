---
id: TASK-49
title: Adopt the Component interface + intent-message routing for Tier 1 TUIs
status: Done
assignee: []
created_date: '2026-05-26 20:33'
labels:
  - feature
  - tui
  - architecture
  - 'slug:feat-tui-component-interface'
  - P3
  - 'closed:resolved'
dependencies: []
priority: low
ordinal: 49000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Resolution

Shipped on main. `pkg/tui/component/` defines the Component interface, shared intent messages (DoneMsg, OpenMsg, LoadMsg), long-running types (LoadingMsg, LoadedMsg, ErrorMsg), and a `Host` helper that wraps a Component for standalone use.

`pkg/tui/chat/` refactored to satisfy the interface: chrome (border + title + status bar) moved out of `Model.View` into a new `standalone.go` host wrapper; direct `tea.Quit` returns replaced with `DoneMsg{}` emits; SetSize/Focus/Blur/Focused/ShortHelp/FullHelp methods added on Model.

Tests in `pkg/tui/chat/standalone_test.go` (golden + interface satisfaction) and `pkg/tui/component/component_test.go` (shared messages, Host wiring). `cmd/sextant-tui-chat-preview/main.go` updated to use the new entry shape.

Building a second Tier 1 component (the ticket's "forcing function") is deferred — best landed against `agents list -i` post-cobra ([[feat-cli-i-flag-tier1-tier2]] is the umbrella).

## Summary

`conventions/tui-conventions.md` (Tier 1 → Component contract) pins
a small interface every component implements beyond `tea.Model`:

```go
type Component interface {
    tea.Model // Init, Update, View

    SetSize(w, h int)
    Focus() tea.Cmd
    Blur()
    Focused() bool

    ShortHelp() []key.Binding
    FullHelp() [][]key.Binding
}
```

Plus a standardized routing model:

- Components emit intents (`DoneMsg{}`, `OpenMsg{Target, ID}`,
  `LoadMsg{ID}`).
- Long-running ops use `LoadingMsg{}`, `LoadedMsg{Result T}`,
  `ErrorMsg{err}`.
- The host (standalone wrapper or `sextant dash`) routes intents
  and decides on `tea.Quit` / focus / pane changes.
- Chrome (titles, borders, status) lives **outside** the component.
  `SetSize` is the content rect.
- Components hold a `client.Client` interface (from `pkg/client/`)
  injected at construction.

Current state:

- `pkg/tui/chat/model.go` is a single `Model` implementing the
  basic `tea.Model` (`Init`, `Update`, `View`). No `SetSize`,
  `Focus`/`Blur`/`Focused`, `ShortHelp`/`FullHelp` methods.
- Chat does not emit intent messages — it directly returns
  `tea.Quit` in some Update paths.
- The chat program (`pkg/tui/chat/program.go`) hosts the model
  and draws its own chrome inline rather than separating model
  from host.
- No shared `LoadingMsg`/`LoadedMsg`/`ErrorMsg` types.

Adopting the interface up front (before there's a second component
to compose) is what makes `sextant dash` (Tier 2) possible without
rewriting chat from scratch.

## Fix shape

1. Add `pkg/tui/component/` defining:
   - `Component` interface as above.
   - Shared intent messages: `DoneMsg`, `OpenMsg{Target, ID}`,
     `LoadMsg{ID}`.
   - Long-running message types: `LoadingMsg`, `LoadedMsg[T]`,
     `ErrorMsg`.
   - A `Host` helper that wraps a `Component` for standalone use
     (draws chrome, owns `tea.Quit`).

2. Refactor `pkg/tui/chat/` to:
   - Implement the `Component` interface on `Model`.
   - Remove chrome (border + title + status) from `Model.View` and
     into `pkg/tui/chat/standalone.go` (the host wrapper).
   - Emit `DoneMsg{}` instead of `tea.Quit`.
   - Accept a `client.Client` at construction (already does via
     `program.go`; confirm shape).
   - Implement `ShortHelp` / `FullHelp` by exposing the existing
     `keys.go` bindings.

3. Build one additional Tier 1 component as a forcing function to
   verify the interface is right (candidate: `agents list -i`,
   refactoring `cmd/sextant-tui-agents/` into `pkg/tui/agents/`).

4. Document the runtime contract: a component must be runnable
   standalone *and* mountable in the dash with no code changes.

## Acceptance

- `pkg/tui/component/` compiles and is documented.
- `pkg/tui/chat/Model` satisfies `component.Component`.
- `pkg/tui/chat/standalone.go` produces the same on-screen output
  as today's `pkg/tui/chat/program.go`, verified by an existing
  golden test.
- `cmd/sextant-tui-chat-preview/main.go` still works against the
  refactored package.
- At least two components (chat + one other) implement the
  interface and pass their respective teatest goldens.

## Related

- `conventions/tui-conventions.md` § "Tier 1: Component TUIs"
- [[feat-cli-i-flag-tier1-tier2]] — depends on this
- [[feat-tui-vhs-fixture-design-loop]] — fixtures share the
  injection point
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-tui-component-interface.md
Discovered in: CLI/TUI conventions adoption
Original created_at: 2026-05-26T20:33-07:00
Resolved at: 2026-05-26T23:15-07:00
<!-- SECTION:NOTES:END -->
