---
title: Pay down errcheck debt from the cobra-fang migration (fmt.Fprintf without _ = …)
status: open
priority: P3
created_at: 2026-05-26T23:10-07:00
labels: [chore, lint, tech-debt, cli]
discovered_in: cobra-fang migration self-reported — the new code uses `fmt.Fprintf`/`Fprintln` directly rather than the `printf`/`println` helpers in the old `output.go`, dropping the return values and creating ~78 new errcheck hits in touched files
---

## Summary

The cobra-fang migration (`8f960c1`) replaced the hand-rolled wrappers in `cmd/sextant/output.go` (which discarded errors internally) with direct `fmt.Fprintf` / `fmt.Fprintln` calls. Functionally correct — `os.Stdout` / `os.Stderr` writes that fail are not recoverable anyway — but the lint count went up because errcheck flags every unhandled error.

Pre-migration lint baseline (per `chore-lint-debt-paydown.md`): ~26 total issues, none from this category.

Post-migration: ~78 new errcheck hits in `cmd/sextant/*.go`. Subagent flagged this for review and did not run `make lint-go` before merging.

## Fix shape

Pick ONE:

1. **Restore the output wrappers.** Re-introduce `mustPrintf`/`mustPrintln` in `cmd/sextant/output.go` that wrap `fmt.Fprintf`/`Fprintln` with `_, _ =` discards. Sweep the new code to call them. Low-churn, idiomatic Go ("the caller can't handle stdout errors").

2. **Sprinkle `_, _ = …`.** Edit each call site directly. Higher churn but no new identifier.

3. **Add a project-wide `//nolint:errcheck` exception for stdout/stderr writes.** Configure `.golangci.yml` to ignore the rule for specific function names. Lowest churn, but hides legitimate errcheck hits elsewhere.

Lean (1) — wrapper keeps the call sites compact and the error-handling decision is centralized.

## Acceptance

- `make lint-go` errcheck count in `cmd/sextant/*.go` drops back to the pre-migration baseline.
- New code in `cmd/sextant/` uses the wrappers (or whichever pattern (1)/(2)/(3) lands).
- Behavior unchanged: stdout writes still go to stdout, stderr to stderr.

## Related

- `[[chore-lint-debt-paydown]]` — pairs with this; both pay down the lint baseline.
- `[[feat-cli-cobra-fang-migration]]` — the source of the debt.
- `[[feat-cli-output-protocol]]` — if it lands soon, restoring `output.go` helpers should align with envelope-wrapping anyway.
