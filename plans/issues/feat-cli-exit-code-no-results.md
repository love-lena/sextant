---
title: Output protocol — add exit code 10 (no results) sentinel + thread through list-style verbs
status: resolved
priority: P3
created_at: 2026-05-27T03:20-07:00
resolved_at: 2026-05-27T03:30-07:00
labels: [feature, cli, output-protocol, follow-up]
discovered_in: feat-cli-output-protocol-wiring landed the data envelope sweep but didn't wire the exit-code-10 path

---

## Resolution

`exitNoResults = 10` lives in `cmd/sextant/main.go` alongside the existing `exitOK`/`exitUser`/`exitSystem`. `errNoResults` is the sentinel verbs return when the query returned zero rows but no actual error occurred; `exitCodeFor` checks for it before any other branch.

`shouldSuppressErrorBanner` recognizes the sentinel too — verbs that return it have already printed their `"no agents"` / `"no pending requests"` line on stdout, so fang's `sextant: <err>` stderr banner would be noise.

Wired into:

- `sextant agents list` — empty result returns errNoResults after printing `"no agents"` (or empty envelope under `--json`).
- `sextant pending list` — same.

`specs/cli/commands.md` § "Exit codes" updated with the row for 10 and the list of verbs that surface it. More list-style verbs (audit query, events tail with `--for`, traces show) can adopt the sentinel as needed; the plumbing is in place.

## Summary

`conventions/tui-conventions.md` (Tier 0 → Exit codes) pins:

> Exit codes carry meaning. 0 success, 1 generic error, 2 usage error (bad flags), **10 = no results found** (distinct from real errors so shell loops can branch on it).

Current state:

- `cmd/sextant/main.go` defines 0 / 1 / 2 only.
- `specs/cli/commands.md` § "Exit codes" lists 0 / 1 / 2.
- No verbs surface "empty result" as a distinct outcome — `agents list` with no agents exits 0 with `"no agents"` to stdout, same as a successful listing.

## Fix shape

1. Add `exitNoResults = 10` to `cmd/sextant/main.go` (alongside the existing `exitOK`/`exitUser`/`exitSystem`).
2. Define `errNoResults` sentinel (or reuse `cliout.NewError(cliout.CodeNoResults, …)`).
3. Update `exitCodeFor` to branch on it.
4. Thread through verbs that can legitimately return zero:
   - `sextant agents list` — empty `resp.Agents`
   - `sextant pending list` — empty pending queue
   - `sextant audit query` — no matches
   - `sextant events tail --for D` — zero envelopes during the window
   - `sextant traces show <id>` — trace doesn't exist
5. `specs/cli/commands.md` § "Exit codes" — add row for 10.

## Acceptance

- `sextant agents list` with no agents exits 10 (was 0).
- `sextant agents list --json` with no agents emits `{data: [], meta:…}` and exits 10.
- `sextant pending list` with empty queue exits 10.
- Shell loops can `if sextant agents list >/dev/null; then …; elif [ $? -eq 10 ]; then echo "empty"; fi`.
- `specs/cli/commands.md` documents exit code 10.

## Related

- `[[feat-cli-output-protocol-wiring]]` — parent; the data envelope sweep paved the way for this.
- `[[feat-cli-output-protocol-tail-and-errors]]` — sibling; error envelope wrapping.
