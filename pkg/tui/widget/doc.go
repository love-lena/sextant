// Package widget holds the shared, surface-agnostic TUI building blocks
// every Tier 1 component composes:
//
//   - ListPane[T]      — generic cursor list (nav, selection, filter, empty)
//   - StreamViewport   — scrollback + tail/autoscroll over bubbles/viewport
//   - DetailPane       — label/value inspector grouped into sections
//   - Source[T] / Pump — data adapter unifying NATS subscribe / file tail /
//     one-shot RPC behind one drain loop
//
// Design per plans/rfc-tui-workstream.md. A widget is a reusable sub-model
// a surface embeds; it is NOT a component.Component (no registry identity).
//
// Receiver convention (load-bearing): widgets use POINTER receivers and
// mutate in place; Update returns an action or tea.Cmd, never a copy of the
// widget. Embedding a value-returning widget inside a pointer Component (how
// component.Host re-asserts tea.Model back to Component) is a copy/staleness
// trap; pointer-in-place sidesteps it.
package widget
