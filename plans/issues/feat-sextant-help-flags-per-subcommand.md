---
title: --help prints empty output for most sextant subcommands
status: wontfix
priority: P3
created_at: 2026-05-26T16:15-07:00
resolved_at: 2026-05-26T22:55-07:00
labels: [feature, cli, ergonomics, polish]
discovered_in: chat TUI Checkpoint C — `sextant conversation --help` was broken; the fix wired conversation's fs.Usage but the other subcommands still emit nothing
---

## Resolution

Obsoleted by `[[feat-cli-cobra-fang-migration]]` — Fang renders
per-command help automatically. Every subcommand in the new RootCmd
tree gets styled, complete `--help` output by virtue of the Cobra +
Fang stack. The original stdlib `flag.FlagSet` / `fs.Usage` boilerplate
problem no longer exists.

## Summary

`sextant <subcmd> --help` now exits cleanly (code 0, no scary error) — but only `sextant conversation --help` prints actual usage text. Every other subcommand (`agents`, `ask`, `audit`, `files`, `exec`, `tail`, `traces`, `pending`, `worktree`, `templates`, `start`, `stop`, `restart`, `status`, `logs`, `doctor`, `init`) exits silently because their `fs.Usage` is the flag-package default which writes to the `io.Discard` sink set by `parseCommonOpts`.

## Fix

Each subcommand has (or should have) a hand-written `*Usage` const at the top of its file. Wire each `runX` to set:

`fs.Usage = func() { fmt.Fprintln(os.Stdout, xUsage) }`

…before calling `parseCommonOpts`. Mechanical — one-liner per subcommand.

A cleaner alternative: extend `parseCommonOpts` to accept a usage string and set `fs.Usage` itself, removing the boilerplate. Trade-off is a wider signature change across ~20 call sites.

## Acceptance

- `sextant <each subcmd> --help` prints its usage text to stdout and exits 0.
- A smoke test in `cmd/sextant/main_test.go` (or per-file) runs each subcommand's runner with `["--help"]` against a discarded writer and asserts `flag.ErrHelp` is returned (or whatever the chosen sentinel is) AND non-empty usage was written.

## Related

- Predecessor commit on this branch that fixed the error message + wired conversation's fs.Usage.
