// Package component defines the Tier 1 TUI component contract from
// `conventions/tui-conventions.md` § "Tier 1: Component TUIs → Component
// contract". Every interactive screen built on `bubbletea` implements
// the Component interface in this package, which lets the same code
// run standalone (under a Host wrapper) or mounted as a pane in
// `sextant dash` (Tier 2).
//
// The package also defines the shared intent-message vocabulary
// (DoneMsg, OpenMsg, LoadMsg) and the long-running-op envelope
// (LoadingMsg, LoadedMsg, ErrorMsg) that components emit and hosts
// route.
package component
