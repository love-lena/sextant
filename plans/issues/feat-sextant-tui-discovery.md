---
title: `sextant tui` Huh-driven menu of available -i surfaces
status: open
priority: P3
created_at: 2026-05-28T10:54-07:00
labels: [feature, cli, tui, discovery]
discovered_in: 2026-05-28 split of feat-cli-i-flag-tier1-tier2 after architecture decisions baked in
---

## Summary

`sextant tui` (no args) opens a Huh-driven menu listing every
Tier 1 component registered via
[[feat-cli-iflag-tier1-components]]'s registry. Selecting an entry
launches the corresponding `sextant <command> -i`.

The menu is built dynamically from `component.Registry.List()` so
new components appear automatically without a menu-update step.

## Shape

1. Add `cmd/sextant/tui.go` — cobra subcommand with no positional
   args.
2. Walk `component.Registry.List()`.
3. Render a `huh.NewSelect[string]()` keyed on `Meta.Name`, with
   `Meta.Description` shown alongside.
4. On selection, exec `os.Args[0] <Meta.Command> -i` (or directly
   invoke the cobra command with the `-i` flag set if exec is
   awkward — implementation detail to discover).
5. Empty registry case: print a helpful message + suggest checking
   the docs.

## Acceptance

- `sextant tui` opens a Huh menu listing all registered components.
- Selecting an entry launches the corresponding `-i` surface.
- `q` / `esc` in the menu exits cleanly.
- New component registration shows up in the menu without code
  changes to `sextant tui`.
- Documented in mdbook under `operator-guide/tui.md` (or similar).

## Why P3

Convenience surface for operators who don't remember which
commands have `-i` modes. Useful but not load-bearing — every
`-i` mode is still reachable via `sextant <command> -i` directly.

## Dependencies

- Hard-blocks on [[feat-cli-iflag-tier1-components]] — needs the
  registry to walk.

## Related

- [[feat-cli-iflag-tier1-components]] — sibling, must land first.
- [[feat-sextant-dash-multipane]] — sibling.
- [[feat-cli-i-flag-tier1-tier2]] — original umbrella (resolved by
  this split).
- [[feat-cli-huh-interactive-confirm]] — same Huh dep; landing
  this after Huh is in go.mod is cheap.
