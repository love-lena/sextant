// Package fixtures houses canned datasets that TUI entry points and
// VHS tapes consume via the hidden --fixture flag, plus the in-memory
// fake bus that serves them.
//
// One fixture is one named, deterministic snapshot of the bus state a
// TUI would see at startup: the list of agents, per-agent frame
// transcripts, and any pending user-input requests. Two layers use the
// same data:
//
//   - teatest tests wire a fixtures.Bus directly into a component's
//     consumer-side interface (chat.Bus, the agent-list bus interface,
//     etc.) so model behavior can be exercised without booting NATS.
//   - VHS tapes and the chat preview binary invoke a TUI-entry command
//     with --fixture <name>; the command swaps the fake bus in for the
//     live *client.Client and runs the same Bubble Tea program against
//     it.
//
// Fixture data is built once at process start and is read-only. The
// fake bus methods are pure projections over that data: RPC reads the
// fixture map and returns a marshalled response; Subscribe returns a
// closed channel populated with the canned frames; PutKV is a no-op.
// Nothing in this package touches the network.
//
// See conventions/tui-conventions.md §"Testing → VHS / Runnable
// mockups" for the convention this package implements.
package fixtures
