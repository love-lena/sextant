---
title: Build pkg/theme/ with role-token vocabulary and base16 YAML themes
status: resolved
priority: P3
created_at: 2026-05-26T20:33-07:00
resolved_at: 2026-05-26T22:34-07:00
labels: [feature, tui, theming]
discovered_in: CLI/TUI conventions adoption
---

## Resolution

Branch: `feat-tui-theme-package-001`

Built `pkg/theme/` with the role-token vocabulary, base16 YAML
loader, icon registry, canonical spinner/progress, and the
`config.toml` schema (`theme = "<name>"`, `icons = "nerd" | "ascii"`)
with the flag > env > file > defaults precedence ladder. Refactored
`pkg/tui/chat/style.go` (now `StylesFor(theme.Theme)`) and
`cmd/sextant-tui-agents/theme.go` (now `themeFor(theme.Theme)`) to
consume roles; cleaned up the leftover bare `lipgloss.Color("8")`
calls in `pkg/tui/chat/view.go` to read from the role table.

`grep -rn 'lipgloss.Color("' pkg/tui/ cmd/sextant-tui-*/` reports
zero hits. Lint clean for files touched (~26 pre-existing lint
issues unchanged elsewhere); `go test ./...` passes.

The `sextant theme list/import/show` Cobra subcommand is **deferred
to the cobra+fang migration wave** (concurrent subagent) per the
ticket dispatch note. Operators can still consume themes today by
dropping a base16 YAML into `~/.config/sextant/themes/` and setting
`theme = "<name>"` in `~/.config/sextant/config.toml`; the
subcommand will only add discovery + import-from-path ergonomics.

## Summary

`conventions/tui-conventions.md` (Theme system + Visual design
language) pins:

- **Source of truth**: `$XDG_CONFIG_HOME/sextant/config.toml` +
  `$XDG_CONFIG_HOME/sextant/themes/*.yaml`. No tinty dependency.
- **Format**: base16-compatible YAML (`base00`–`base0F`).
- **Role tokens**: every Lipgloss style reads roles, not palette
  slots:
  - Structural: `bg`, `bg_alt`, `fg`, `fg_muted`, `border`,
    `border_active`
  - Signal: `accent`, `danger`, `warning`, `success`
- **Icons**: Nerd Font default, ASCII fallback functionally usable.
  `config.icons = "nerd" | "ascii"`. Icon column always reserved.
- **`sextant theme import <path>`** copies a base16 file into the
  themes dir.

Current state:

- `pkg/theme/` does not exist. `conventions/tui-conventions.md`
  has referenced it as the intended home for theme tokens since
  the original tui-conventions doc; it's a long-running gap.
- `pkg/tui/chat/style.go:46-63` hardcodes `lipgloss.Color("4")`,
  `lipgloss.Color("8")`, `lipgloss.Color("15")` etc. — bare ANSI
  palette indices.
- `cmd/sextant-tui-agents/theme.go:34-41` has the same shape:
  `lipgloss.Color("12")`, `lipgloss.Color("9")`, `lipgloss.Color("8")`.
- No icon abstraction; current TUIs avoid icons entirely.
- `~/.config/sextant/theme.toml` is mentioned in the old tui-
  conventions but unimplemented.

## Fix shape

1. Create `pkg/theme/` with:
   - `theme.go` — the `Theme` struct holding all role tokens as
     `lipgloss.Color` (or `lipgloss.AdaptiveColor` for the no-theme
     default).
   - `base16.go` — load a base16 YAML file and map slots to roles.
     Default mapping documented inline.
   - `icons.go` — `Icon{Nerd, ASCII string}` with a single registry
     declared in one place. Selection driven by config.
   - `defaults.go` — the built-in adaptive theme that ships when no
     theme file is present.
   - `theme_test.go` — golden tests for the slot→role mapping for
     a known base16 theme (e.g. tomorrow-night).

2. Add `pkg/theme/spinner.go` and `pkg/theme/progress.go` exposing
   the **one canonical spinner** and **one canonical progress
   style**, as the conventions doc requires.

3. Refactor `pkg/tui/chat/style.go` to consume roles from
   `pkg/theme/` instead of bare palette indices. Same for
   `cmd/sextant-tui-agents/theme.go`. Remove every bare
   `lipgloss.Color("N")` in TUI code.

4. Add `sextant theme` Cobra subcommand:
   - `sextant theme list` — show themes in the themes dir.
   - `sextant theme import <path>` — copy a base16 YAML in.
   - `sextant theme show [name]` — preview the role mapping.

5. Add `theme = "<name>"` and `icons = "nerd" | "ascii"` keys to
   `~/.config/sextant/config.toml`. Config precedence per the doc:
   flag > `SEXTANT_*` env > config file > defaults.

## Acceptance

- `grep -rn 'lipgloss.Color("' pkg/tui/ cmd/sextant-tui-*/` returns
  zero hits outside `pkg/theme/`.
- `sextant theme import ~/Downloads/tomorrow-night.yaml` writes the
  file into `~/.config/sextant/themes/`.
- `SEXTANT_THEME=tomorrow-night sextant agents list -i` renders
  with the imported palette.
- `config.icons = "ascii"` swaps every icon to its ASCII equivalent
  without shifting layout.

## Open

- Should the role vocabulary expose `info` separately from
  `accent`, or is `accent` enough? The old tui-conventions referenced
  `Info` as a fifth signal; the new doc collapses it into
  `accent`. Lean collapse — one signal color per screen is the
  intent.

## Related

- `conventions/tui-conventions.md` § "Theme system" + "Visual
  design language → Icons"
- `pkg/tui/chat/style.go` (current bare-palette site)
- `cmd/sextant-tui-agents/theme.go` (current bare-palette site)
