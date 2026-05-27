---
title: Wire `sextant theme list/import/show` Cobra subcommand
status: resolved
priority: P3
created_at: 2026-05-26T23:10-07:00
resolved_at: 2026-05-27T04:15-07:00
labels: [feature, cli, theme, follow-up]
discovered_in: cobra-fang migration — pkg/theme/ landed in a sibling worktree commit while the cobra subagent was migrating cmd/sextant/, so the agent skipped wiring the `sextant theme` subcommand to avoid races
---

## Resolution

`cmd/sextant/theme.go` ships `sextant theme list`, `sextant theme import <path>`, and `sextant theme show [name]`. All three respect the standard `--json` envelope contract (via `writeJSON`). `theme list` against an empty / missing themes dir returns `errNoResults` (exit code 10), matching the convention.

`theme import` validates the file parses as base16 YAML (`theme.ParseBase16`) before writing into `$XDG_CONFIG_HOME/sextant/themes/`. `theme show` accepts an optional theme name; with no name it shows the built-in default.

`cmd/sextant/root_test.go` covers the cobra wiring (3 new entries in the verb-path matrix). Lint clean (gofumpt-formatted, gosec-suppressed for the operator-supplied import path).

The `--json` shape for `theme show` lists only the role names (not the resolved hex codes) because `lipgloss.TerminalColor` is an interface without a stable hex accessor. Filed as a small follow-up [[feat-theme-show-hex-codes]] if richer JSON output becomes valuable.

## Summary

`pkg/theme/` shipped on commit `7e36bef` with the full role-token / base16 / icon machinery. The original ticket `feat-tui-theme-package.md` listed a Cobra subcommand surface:

- `sextant theme list` — show themes in `$XDG_CONFIG_HOME/sextant/themes/`.
- `sextant theme import <path>` — copy a base16 YAML in.
- `sextant theme show [name]` — preview the role mapping.

These weren't wired during the cobra-fang migration (`8f960c1`) because `pkg/theme/` landed on main while that subagent was already mid-flight. Adding them now is a follow-up.

## Fix shape

1. New file `cmd/sextant/theme.go` modeled on the existing `cmd/sextant/templates.go` (a sibling resource-noun verb).
2. Cobra subcommand tree:
   - `theme list` — read `$XDG_CONFIG_HOME/sextant/themes/`, print each name + path.
   - `theme import <path>` — copy the file into the themes dir; validate it's parseable base16 YAML via `pkg/theme/base16.go` before accepting.
   - `theme show [name]` — load the named theme (or active default), render a preview block showing the role tokens with their resolved colors.
3. Register under `cmd/sextant/root.go`'s RootCmd tree alongside `agents`, `pending`, etc.
4. `--json` support on `list` and `show` (envelope per `conventions/tui-conventions.md`).
5. Tests: `cmd/sextant/theme_test.go` covers wiring (`RootCmd.Find([]string{"theme", "list"})`) + the import-file path with a tempdir.

## Acceptance

- `sextant theme list` shows the bundled defaults + any user-installed themes.
- `sextant theme import ~/Downloads/tomorrow-night.yaml` writes the file into `$XDG_CONFIG_HOME/sextant/themes/` and exits 0.
- `sextant theme show tomorrow-night` prints a role table with rendered swatches.
- All commands respect the standard `--json` envelope contract from `[[feat-cli-output-protocol]]`.

## Related

- `[[feat-tui-theme-package]]` — pkg/theme/ source.
- `[[feat-cli-cobra-fang-migration]]` — RootCmd home for the new subcommand.
- `[[feat-cli-output-protocol]]` — envelope schema for `--json` output.
