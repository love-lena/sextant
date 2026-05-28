---
title: Wire -i flag for Tier 1 component TUIs, sextant tui launcher, and Tier 2 dash
status: resolved
priority: P3
created_at: 2026-05-26T20:33-07:00
resolved_at: 2026-05-28T10:54-07:00
labels: [feature, cli, tui, architecture]
discovered_in: CLI/TUI conventions adoption
---

## Resolution (2026-05-28)

Decisions baked in during the 2026-05-28 walkthrough. Split into
three implementation tickets that can land independently:

- **[[feat-cli-iflag-tier1-components]]** — `init()`-time registry
  in `pkg/tui/component/` + `-i` flag on Tier 1 cobra commands.
- **[[feat-sextant-tui-discovery]]** — `sextant tui` Huh menu
  driven by the registry.
- **[[feat-sextant-dash-multipane]]** — `sextant dash` with
  Stickers + BubbleZone (mouse-on by default). Default config TOML
  at `cmd/sextant/dash-default-config.toml` (embedded via
  `//go:embed`), `~/.config/sextant/config.toml` override,
  `--dump-default-config` flag to print the embedded default.

Decisions captured:

- Dash composes registered components automatically; embedded
  default TOML + user override (with `--dump-default-config` to
  print the default). The TOML lives at
  `cmd/sextant/dash-default-config.toml` so contributors can
  reference + copy it directly.
- Components self-register via `init()` rather than a central
  registry file.
- `sextant tui` reads the registry (not hard-coded).
- BubbleZone ships in v1 — mouse-on by default in the dash.

## Original open questions (preserved for context)

This ticket bundles three architecturally-distinct pieces (Tier 1 `-i` flag, Tier 1 discovery `sextant tui`, Tier 2 `sextant dash`) that share infrastructure but make different design tradeoffs. Foundations now landed (`pkg/theme`, `pkg/tui/component`, `pkg/fixtures`, cobra-fang); ready to decide how to land the rest.

Open questions worth answering before implementation:

1. **Tier 2 dash pane layout config** — `~/.config/sextant/config.toml` `[[dash.panes]]` table per the ticket, or a more discovery-friendly default that just composes the registered Tier 1 components?
2. **Tier 1 component registry** — `init()`-time self-registration (each component package registers itself when imported) vs. an explicit registry file. The first is more decentralized; the second is more grep-able.
3. **`sextant tui` menu sourcing** — read the registry, or hard-code the v1 surface (agents, chat, pending, traces) and grow as new components ship?
4. **BubbleZone for click regions** — pull in `github.com/lrstanley/bubblezone` now or defer mouse support to a follow-up? The ticket pins mouse-on by default.

Once these answers exist this ticket can split into 2-3 implementation sub-tickets (Tier 1 surface, `sextant tui` discovery, `sextant dash`). For now the foundations are ready; this ticket holds the open design questions.

## Summary

`conventions/tui-conventions.md` (Architecture → three tiers) defines:

- **Tier 0**: CLI base. Every command, plain output, scriptable.
  Already in place.
- **Tier 1**: Component TUIs. `-i` (or `--tui`) flag on a CLI
  command launches a single-purpose interactive screen for that
  command. `sextant tui` (no args) is a Huh-driven listing that
  enumerates available `-i` surfaces.
- **Tier 2**: `sextant dash`. Flagship multi-pane TUI built on
  Stickers + BubbleZone, composing Tier 1 components as panes.

Current state:

- No `-i` flag on any command.
- No `sextant tui` command.
- No `sextant dash`.
- `cmd/sextant-tui-agents/` and `cmd/sextant-tui-chat-preview/`
  exist as standalone TUI binaries — they're the right
  building-block precedent but live outside the `-i` entry path.

This ticket covers all three tiers' wiring as one body of work,
because the surface area only makes sense composed. Land Tier 1
first; Tier 2 follows once 3–4 components exist.

## Dependencies

Hard-blocks:
- [[feat-tui-component-interface]] — every `-i` surface mounts a
  `Component`.
- [[feat-tui-theme-package]] — the dash and components share theme
  roles.
- [[feat-cli-cobra-fang-migration]] — `-i` and `sextant tui` use
  Cobra command wiring.
- [[feat-tui-vhs-fixture-design-loop]] — fixtures power
  development of the panes without a live daemon.

## Fix shape

### Tier 1: `-i` flag

1. Add a `--tui` / `-i` flag to commands that have a meaningful
   interactive surface:
   - `sextant agents list -i` → mount `pkg/tui/agents.Model`
   - `sextant agents show <id> -i` → mount the same, focused on
     `<id>`
   - `sextant conversation <agent>` (already TUI-shaped) →
     formalize the `-i`-equivalent
   - `sextant pending list -i` → mount `pkg/tui/pending.Model`
   - `sextant traces show <trace_id> -i` → trace explorer

2. The flag's handler hosts the component with the standalone
   `Host` wrapper from [[feat-tui-component-interface]]. The
   positional arg (e.g. `<id>`) seeds a `LoadMsg` into `Init`.

### Tier 1 discovery: `sextant tui`

1. `sextant tui` (no args) prints a Huh-driven menu listing every
   `-i`-enabled command with a short description. Selecting one
   launches `sextant <command> -i`.

2. The list is built from a registry — each component registers
   itself at `init()` time with name, description, and the command
   path to invoke. No separate static menu file.

### Tier 2: `sextant dash`

1. Add `cmd/sextant/dash.go` (or as a Cobra subcommand) wiring
   Stickers for flex layout + BubbleZone for click regions. Mouse
   on by default.

2. The dash composes existing Tier 1 components as panes — does
   not reimplement them. Pane layout configurable via
   `~/.config/sextant/config.toml`:

   ```toml
   [[dash.panes]]
   id = "agents"
   command = "agents list"

   [[dash.panes]]
   id = "conversation"
   command = "conversation $selected_agent"
   ```

3. Inter-pane routing uses the `OpenMsg{Target, ID}` /
   `LoadMsg{ID}` convention from the conventions doc. The
   `$selected_agent` template variable resolves against the
   `ui.state.<operator>.selected_agent` NATS KV key (per the
   sextant-specific shared-state section of the doc).

4. Persistent status/help footer; `Tab`/`Shift+Tab` cycles pane
   focus; numbered keys (1-9) jump to a numbered pane.

5. Add `tests/visual/dash_default.tape` to exercise the canonical
   layout under VHS.

## Acceptance

- `sextant agents list -i` opens an interactive agents list,
  exits cleanly on `q`.
- `sextant tui` opens a Huh menu of interactive surfaces.
- `sextant dash` opens a multi-pane TUI composing at least
  agents + conversation, with `Tab` switching focus.
- Selecting an agent in the agents pane sends `OpenMsg{Target:
  "agent", ID: ...}`, the dash resolves it to the conversation
  pane, and the conversation pane loads the agent's frames.
- No code in `pkg/tui/chat/` or `pkg/tui/agents/` is dash-aware
  — they're hosted the same way standalone or in-dash.

## Related

- `conventions/tui-conventions.md` § "Architecture: three tiers" +
  "Tier 1: Component TUIs" + "Tier 2: sextant dash"
- [[feat-tui-component-interface]]
- [[feat-tui-theme-package]]
- [[feat-cli-cobra-fang-migration]]
- [[feat-tui-vhs-fixture-design-loop]]
