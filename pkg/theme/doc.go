// Package theme is sextant's single source of truth for visual roles,
// palette tokens, icons, and shared TUI primitives (spinner, progress).
//
// Background. `conventions/tui-conventions.md` § "Theme system" and
// § "Visual design language" pin three rules that this package
// enforces:
//
//  1. Every Lip Gloss style reads role tokens, never palette slots
//     or bare ANSI indices. Roles split into structural
//     (bg, bg_alt, fg, fg_muted, border, border_active) and signal
//     (accent, danger, warning, success).
//  2. Themes are base16-compatible YAML (`base00`–`base0F`) loaded
//     from `$XDG_CONFIG_HOME/sextant/themes/`. The default mapping
//     from slots to roles is documented inline in base16.go.
//  3. Icons live in one registry — each icon declares both a Nerd
//     Font glyph and an ASCII fallback. Selection is driven by
//     config.
//
// Public surface:
//
//   - Theme: the role-token bundle.
//   - DefaultTheme(): the adaptive theme that ships when no theme file
//     is present.
//   - LoadBase16: parse a base16 YAML and return a Theme.
//   - Icons: the per-mode icon registry.
//   - NewSpinner / NewProgress: the one canonical spinner / progress
//     style, themed.
//
// No package outside `pkg/theme/` should construct `lipgloss.Color`
// values for chrome. CLI / TUI code consumes roles and icons.
package theme
