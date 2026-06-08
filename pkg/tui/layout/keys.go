package layout

// Layout-level shortcut keys. The core theme.Keymap carries the locked
// interaction set (nav, step-in/out, options, quit) shared by every dash
// surface; preset-switch and detail-toggle are *layout-only* actions with no
// surface meaning, so they live here as the layout's own overridable shortcuts
// rather than bloating the shared keymap. They are still data — named string
// constants read in one place — not literals scattered through the switch, and
// every action they reach is also available from the options menu (the keymap's
// `o`), so the shortcuts are pure convenience.
//
// These are defaults and expected to churn; a future host-override path can
// replace them the way theme.Keymap.Merge replaces the core bindings.
const (
	// detailToggleKey toggles the detail-on-demand pane in and out at the layout
	// level (also reachable from the options menu).
	detailToggleKey = "d"
	// presetCycleKey cycles to the next built-in preset at the layout level (also
	// reachable from the options menu).
	presetCycleKey = "p"
)
