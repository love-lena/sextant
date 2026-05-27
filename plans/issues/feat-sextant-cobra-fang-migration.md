---
title: Migrate sextant CLI from stdlib flag to cobra + charmbracelet/fang
status: wontfix
priority: P3
created_at: 2026-05-26T17:20-07:00
resolved_at: 2026-05-26T21:30-07:00
labels: [feature, cli, polish, ergonomics, charm]
discovered_in: chat TUI Checkpoint C — `--help` audit raised the question of whether sextant should use charm's CLI tooling to match the TUI
---

## Duplicate

Superseded by [[feat-cli-cobra-fang-migration]] — same scope, same fix shape, more current cross-links (destructive flags, output protocol, resource-verb cleanup). Tracking continues there.

## Summary

`sextant` is built on Go's stdlib `flag` package with hand-written `*Usage` const strings per subcommand. The TUI uses charm (`bubbletea`, `lipgloss`, `bubbles`). The two halves of the operator-facing surface have different aesthetics and different ergonomics. Migrating the CLI to [`spf13/cobra`](https://github.com/spf13/cobra) + [`charmbracelet/fang`](https://github.com/charmbracelet/fang) brings them into one design language and unlocks features stdlib `flag` doesn't provide.

## Why P3

Polish, not function. The current CLI works. `--help` output is plain but readable, exit codes are right, flag parsing is correct. The migration cost is real (19 subcommands, ~3000 LOC of CLI code) and the gain is incremental UX improvement. Worth doing eventually for ecosystem consistency with the TUI, but no operator workflow is blocked by the current state.

## What this unlocks

- **`fang`'s styled help output** — colored sections, flag groups, version display, error formatting that matches the charm aesthetic. Consistent with the TUI.
- **cobra-native features** — subcommand suggestions on typo (`sextant conversaton` → "did you mean `conversation`?"), shell completion (bash/zsh/fish), markdown / man-page generation, structured persistent flags, command aliases.
- **Better testability of CLI surface** — cobra's `Command.SetArgs` + `Execute` model is easier to unit-test than the current `runX(ctx, args)` pattern with stdlib flag.
- **Removes per-command flag-parsing boilerplate** — current code reorders flags before positional args via `reorderFlagsBeforePositional` (a workaround for stdlib flag's "stop at first non-flag" behavior). Cobra handles this natively.

## Migration scope

The full sextant CLI command tree (current state):

- top-level: `init`, `doctor`, `start`, `stop`, `restart`, `status`, `logs`, `ask`, `tail`, `audit`, `traces`, `exec`, `version`, `help`
- `agents` — `list`, `show`, `spawn`, `kill`, `restart`, `prompt`, `archive`
- `conversation` — single verb with flags (`--tail`, `--from-seq`, `--json`, `--read`)
- `pending` — `list`, `answer`, `defer`, `escalate`
- `files` — `read`, `list`, `tail`
- `worktree` — `list`, `create`, `destroy`, `merge`, `diff`
- `templates` — `reload`

Plus the existing common-options pattern (`--config-dir`, `--json`) shared across all verbs — cleanly modeled as cobra persistent flags on the root command.

## What needs to be careful

- **Exit codes.** The current behavior is 0 / 1 (user error) / 2 (system error) per `specs/cli/commands.md`. Cobra defaults to 1 on any error; we need to wire `RunE` returns to the existing `exitCodeError` shape and have main translate. The recently-merged daemon-lifecycle work and `[[feat-sextant-help-flags-per-subcommand]]` both depend on this — preserve invariants.
- **`--json` flag.** Every command supports `--json` for scriptable output. Model it as a persistent flag on the root. Audit each command's existing implementation to ensure no regression in NDJSON shape (`sextant conversation --json` in particular must stay byte-identical for piped consumers — the chat TUI lives alongside it precisely for this reason).
- **The `reorderFlagsBeforePositional` workaround.** Once on cobra, the workaround disappears. Verify all flag-after-positional usages still work after migration (this is the `sextant agents spawn <name> --template T` shape).
- **Common-options helper (`parseCommonOpts`).** Gets replaced by cobra persistent flags + a small accessor. All ~20 call sites change shape.
- **Test surface.** Existing `*_test.go` files exercise the CLI via `run(ctx, args)`. Cobra makes this cleaner via `rootCmd.SetArgs` — but the existing tests need updating en masse.

## Recommended approach

Phased — don't migrate all 19 subcommands in one PR.

1. **Spike phase (small):** wire cobra + fang at the root with one leaf subcommand (`conversation`, since it just got attention and has a clean fs.Usage already). Validate exit codes, `--json` propagation, the chat TUI launch path, and `--help` rendering. Land as a small PR.
2. **Migrate one verb family per PR.** Agents next (has nested subcommands — good test of cobra's tree handling). Then pending, files, worktree, templates. Each PR also updates that verb's tests.
3. **Migrate top-level singletons last** — `init`, `doctor`, `start`/`stop`/`restart`/`status`/`logs`/`ask`/`tail`/`audit`/`traces`/`exec`. These are mostly independent; bulk-migrate when the patterns are settled.
4. **Remove the stdlib flag scaffolding.** Once nothing imports `parseCommonOpts` / `reorderFlagsBeforePositional`, delete them.
5. **Make `[[feat-sextant-help-flags-per-subcommand]]` obsolete** — fang renders the per-command help automatically; the hand-written `*Usage` consts go away in favor of cobra's `Long` / `Example` fields.

## Acceptance

- All 19 subcommands work as before, with identical exit codes and `--json` output shape.
- `sextant <subcmd> --help` renders fang-styled help for every subcommand.
- `sextant <typo>` suggests the right subcommand.
- Shell completion generation works: `sextant completion bash | source` (and zsh, fish).
- Existing `cmd/sextant/*_test.go` files updated to use cobra's `SetArgs` + `Execute`; all green.
- `[[feat-sextant-help-flags-per-subcommand]]` is closed by this migration (mark as superseded once that issue's `--help` gap is fully addressed via fang).

## Open questions

1. Adopt `fang` from day one, or migrate to cobra first and add fang in a follow-up? Recommend day one — fang is the whole point.
2. Use cobra's [persistent pre-run hooks](https://pkg.go.dev/github.com/spf13/cobra#hdr-PreRun_and_PostRun) for the daemon-connection setup that `connectAgent` currently handles inline? Cleaner but changes the call pattern.
3. Markdown / man-page generation via cobra — worth wiring into `make docs` for the mdbook?

## Related

- `[[feat-sextant-help-flags-per-subcommand]]` — narrower fix, made obsolete by this migration.
- `conventions/tui-conventions.md` §"CLI conventions" — current conventions stay valid post-migration, just expressed via cobra.
- `pkg/tui/chat/` — uses bubbletea/lipgloss/bubbles; the CLI matching that ecosystem closes the consistency gap.
