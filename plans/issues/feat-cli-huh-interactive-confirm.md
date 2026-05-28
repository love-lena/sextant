---
title: Add Huh interactive confirmation to destructive verbs (TTY path)
status: open
priority: P3
created_at: 2026-05-26T23:50-07:00
labels: [feature, cli, safety, polish, charm]
discovered_in: implementing feat-cli-destructive-op-flags — shipped --dry-run + --yes but skipped the TTY+Huh interactive confirm; charmbracelet/huh isn't in go.mod yet
---

## Decision (2026-05-27)

**Ship Huh.** Decision made 2026-05-27 during the dispatch-readiness
walkthrough. `charmbracelet/huh` joins go.mod; destructive verbs
(`agents stop`, `agents restart`, `agents archive` incl.
`--all-dead`, `daemon stop`, `daemon restart`) render a TTY confirm
on stdin when no `--yes` / `--dry-run` is set.

The destructive-verb set above already reflects the verb-migration
(PR #12 — `kill` → `stop`).

Implementation is mechanical now that the dep call is made; ready
to dispatch as a subagent. The "Fix shape" section below describes
the wiring against `cmd/sextant/destructive.go::destructiveFlags.confirm`.

## Summary

`feat-cli-destructive-op-flags` shipped the safety baseline: `--dry-run` and `--yes` on the five destructive verbs (`agents kill`, `agents restart`, `agents archive` incl. `--all-dead`, `daemon stop`, `daemon restart`). What it didn't ship is the conventions doc's TTY-aware refinement:

> Destructive ops need `--dry-run` and confirmation. **TTY: confirm via Huh.** Non-TTY: require `--yes`. `--dry-run` prints what would happen, exits 0.

Currently, an interactive operator without `--yes` gets a structured refusal naming the flag rather than an inline confirmation prompt. The safety property is preserved (no accidental destruction) but the UX is rougher than the conventions doc pins.

## Fix shape

1. `go get github.com/charmbracelet/huh` (not in go.mod today despite the cobra-fang migration nominally pulling it).
2. Extend `destructiveFlags.confirm` in `cmd/sextant/destructive.go`:
   - If `--yes` set → proceed (current behavior).
   - If `--dry-run` set → preview + exit 0 (current behavior).
   - Else, check `isatty.IsTerminal(os.Stdin.Fd())`:
     - TTY → render `huh.NewConfirm().Title(...).Description(...)` with the action label; on Yes proceed, on No abort cleanly.
     - Non-TTY → return `errDestructiveNoYes` naming the flag (current behavior).
3. Tests: add a TTY-mock scenario verifying the Huh prompt is invoked (use `huh`'s test harness if available, otherwise a fake input reader).

## Acceptance

- `sextant agents kill foo` from a real terminal renders a Huh confirm — `Yes` proceeds, `No` exits 0 with no RPC issued.
- `sextant agents kill foo` from a pipe (no TTY) still errors with `destructive op requires --yes`.
- `sextant agents kill foo --yes` is byte-identical to today (proceeds without prompting).
- `sextant agents kill foo --dry-run` is byte-identical to today (prints `[dry-run] would …`).

## Related

- `[[feat-cli-destructive-op-flags]]` — the baseline this builds on.
- `conventions/tui-conventions.md` § "Command design → Destructive ops" — the convention this completes.
