---
id: TASK-44
title: Migrate cmd/sextant from stdlib flag to Cobra + Fang + charmbracelet/log
status: Done
assignee: []
created_date: '2026-05-26 20:33'
labels:
  - feature
  - cli
  - framework-migration
  - 'slug:feat-cli-cobra-fang-migration'
  - P3
  - 'closed:resolved'
dependencies: []
priority: low
ordinal: 44000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Resolution

Landed on branch `feat-cli-cobra-fang-resource-verb-001` together with
`[[feat-cli-resource-verb-cleanup]]` — the migration vehicle was a single
RootCmd tree built directly in resource-verb shape (no intermediate
stdlib-shape rename). Summary:

- `go.mod` now requires `github.com/spf13/cobra`,
  `github.com/charmbracelet/fang`, `github.com/charmbracelet/log`,
  `github.com/charmbracelet/huh`.
- `cmd/sextant/main.go` is a thin Fang entry point; `cmd/sextant/root.go`
  builds the RootCmd tree, persistent flags (`--config-dir`,
  `--data-dir`, `--json`, `-v`, `--no-color`), and the two
  `charmbracelet/log` loggers (`userLog` → stderr always, `diagLog` →
  stderr when `-v`).
- Every verb file is cobra-native; `reorderFlagsBeforePositional` and
  `parseCommonOpts` are deleted (Cobra parses flags interleaved with
  positionals natively). `output.go`'s `printf`/`println` helpers are
  retained only where init.go still emits progress lines; no new code
  uses them.
- Wiring test at `cmd/sextant/root_test.go::TestRootCmdWiring` asserts
  every documented command path resolves.
- Exit codes preserved (0 / 1 user / 2 system). Fang's
  `WithErrorHandler` is configured to suppress the banner for
  exec/status sentinels and otherwise render `sextant: <err>` on stderr.

## Summary

`conventions/tui-conventions.md` (Tier 0) pins the CLI framework as:

- **Cobra + Fang** for command structure (Fang styles help/errors/version)
- **`charmbracelet/log`** with two loggers (user-facing + diagnostic `-v`)
- **Huh** for one-shot prompts in CLI flows (confirms, single inputs)
- **Glamour** for markdown-flavored content (long help, embedded docs)

`cmd/sextant/` currently uses Go stdlib `flag.FlagSet` with a custom
`reorderFlagsBeforePositional` helper (`cmd/sextant/agents.go:118`) to
work around stdlib `flag` stopping at the first non-flag arg. Output
goes through hand-rolled `printf`/`println` helpers in
`cmd/sextant/output.go`.

The current setup works but every new command pays the same custom
parsing tax, and help / version output is bare. Migration unlocks
consistent styling, subcommand autocompletion, and a real logger
behind the `-v` flag instead of ad-hoc stderr writes.

## Fix shape

1. Add `github.com/spf13/cobra` + `github.com/charmbracelet/fang` to
   `go.mod`.
2. Add `github.com/charmbracelet/log` and configure two loggers
   (one for user-facing messages → stderr, one for `-v` diagnostics
   gated on a global flag).
3. Add `github.com/charmbracelet/huh` for `--yes`-confirm flows (see
   [[feat-cli-destructive-op-flags]]).
4. Convert top-level dispatch in `cmd/sextant/main.go` to a Cobra
   `RootCmd` with one `cobra.Command` per current verb (init, doctor,
   start, stop, restart, status, logs, agents, conversation, ask,
   pending, files, exec, audit, tail, traces, worktree, templates).
5. Convert nested verbs (`agents <verb>`, `pending <verb>`, etc.) to
   nested `cobra.Command` trees. Drop `reorderFlagsBeforePositional`
   — Cobra parses flags interleaved with positionals natively.
6. Wire Fang at the root to style help / errors / version. Remove the
   hand-rolled `printUsage` in `cmd/sextant/main.go:105`.
7. Replace `output.go` `printf`/`println` helpers with the
   user-facing `charmbracelet/log` logger. Per the conventions doc:
   **stdout = data, stderr = messages, never mixed.**

## Migration order

Land the framework swap behind a single PR per surface so individual
commands stay green. Suggested order: init → doctor (small surfaces,
already mostly text) → status/start/stop/restart → agents (largest)
→ everything else.

## Acceptance

- `go.mod` lists cobra, fang, charmbracelet/log, huh.
- `sextant --help` is Fang-styled.
- No imports of `flag` remain under `cmd/sextant/`.
- `reorderFlagsBeforePositional` is deleted.
- All existing tests in `cmd/sextant/*_test.go` still pass.
- One new test per migrated command verifies the Cobra wiring (e.g.
  `RootCmd.Find([]string{"agents", "list"})` returns the expected
  command).

## Related

- `conventions/tui-conventions.md` § "Tier 0: CLI base"
- [[feat-cli-destructive-op-flags]] — Huh confirms depend on this
- [[feat-cli-output-protocol]] — JSON envelope work can ride the
  same migration
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-cli-cobra-fang-migration.md
Discovered in: CLI/TUI conventions adoption
Original created_at: 2026-05-26T20:33-07:00
Resolved at: 2026-05-26T22:55-07:00
<!-- SECTION:NOTES:END -->
