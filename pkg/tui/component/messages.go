package component

// Intent messages — components emit these; hosts decide what they
// mean. From `conventions/tui-conventions.md` § "Cross-component
// routing": components don't address each other, they emit intents
// and the dash (or standalone host) routes.

// DoneMsg signals the component is finished and the host should tear
// down. Standalone hosts translate this to tea.Quit; the dash
// translates it to "close this pane".
type DoneMsg struct{}

// OpenMsg asks the host to surface another resource — typically a
// different pane, or a different component instance scoped to ID.
// Target is a stable string ("agent", "decision", …); ID identifies
// the resource within that target's space. The host's router decides
// how to resolve Target.
type OpenMsg struct {
	Target string
	ID     string
}

// LoadMsg tells a component to (re)load its data scoped to ID. The
// standalone host fires one LoadMsg at startup; the dash fires it
// when routing an OpenMsg from a sibling pane. The receiving
// component handles LoadMsg the same way in both cases.
type LoadMsg struct {
	ID string
}

// Long-running-op envelope — components emit these around any async
// work (RPC fetch, log tail, KV watch). The host's renderer can show
// a spinner on LoadingMsg, the result on LoadedMsg, an error banner
// on ErrorMsg. Standardized so the dash can render them uniformly
// across panes.

// LoadingMsg announces an async op has started. The component (or
// host) can show a spinner / placeholder until LoadedMsg or ErrorMsg
// arrives.
type LoadingMsg struct{}

// LoadedMsg carries the result of an async op. Result is `any` rather
// than a generic type parameter so the message round-trips through
// `tea.Msg` (which is itself `any`) without a per-type wrapper at
// every dispatch site. The receiving component type-asserts Result
// to its expected payload.
type LoadedMsg struct {
	Result any
}

// ErrorMsg announces an async op failed. The host decides surface —
// inline banner in standalone, toast in the dash. Components should
// not crash on ErrorMsg; they should render the previous state and
// let the operator dismiss.
type ErrorMsg struct {
	Err error
}
