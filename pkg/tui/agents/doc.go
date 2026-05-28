// Package agents holds the Bubble Tea Component for the agents list
// TUI. The model lists every AgentDefinition (via list_agents RPC),
// re-fetches on `agents.*.lifecycle` envelopes, counts pending
// user-input requests, and writes the cursor's UUID to the
// `ui_state.<operator>.selected_agent` KV on Enter.
//
// The Component lives here (rather than in cmd/sextant-tui-agents/)
// so the same model can run:
//
//   - Standalone via `cmd/sextant-tui-agents/` (the legacy binary,
//     thin main.go that wraps NewStandalone).
//   - Under `sextant agents list -i` via component.Host.
//   - Mounted in `sextant dash` (Tier 2) once the multipane lands.
//
// Conventions: see `conventions/tui-conventions.md`. The package
// self-registers with `pkg/tui/component`'s registry via init() in
// register.go so discovery surfaces (`sextant tui`, `sextant dash`)
// can walk it.
package agents
