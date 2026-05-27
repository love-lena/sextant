---
title: Migrate cmd/sextant from stdlib flag to Cobra + Fang + charmbracelet/log
status: open
priority: P3
created_at: 2026-05-26T20:33-07:00
labels: [feature, cli, framework-migration]
discovered_in: CLI/TUI conventions adoption
---

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
