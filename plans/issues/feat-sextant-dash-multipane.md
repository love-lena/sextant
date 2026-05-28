---
title: `sextant dash` flagship multi-pane TUI — Stickers layout + BubbleZone mouse
status: open
priority: P2
created_at: 2026-05-28T10:54-07:00
labels: [feature, cli, tui, dash, architecture]
discovered_in: 2026-05-28 split of feat-cli-i-flag-tier1-tier2 after architecture decisions baked in
---

## Summary

`sextant dash` opens a flagship multi-pane TUI that composes
registered Tier 1 components as panes. Stickers handles flex
layout; BubbleZone handles click regions (mouse-on by default).

Pane layout comes from a default TOML embedded in the binary; an
operator override at `~/.config/sextant/config.toml` takes
precedence when present.

## Shape

### 1. Default config TOML (in-repo, embedded)

File: `cmd/sextant/dash-default-config.toml`

Contents (initial):

```toml
# Default sextant dash layout. Override at
# ~/.config/sextant/config.toml; same schema. Print this default
# via `sextant dash --dump-default-config`.

[[dash.panes]]
id = "agents"
command = "agents list"

[[dash.panes]]
id = "conversation"
command = "conversation $selected_agent"

[[dash.panes]]
id = "pending"
command = "pending list"
```

Embedded into the binary via `//go:embed dash-default-config.toml`.
The file lives next to `cmd/sextant/dash.go` so contributors can
reference + copy it without spelunking through the binary.

### 2. Config loader

Look for `~/.config/sextant/config.toml` first; fall back to the
embedded default. New flag: `sextant dash --dump-default-config`
prints the embedded TOML to stdout (so operators can pipe it into
their config file as a starting template).

### 3. Dash command

`cmd/sextant/dash.go`:

- Builds the layout via `github.com/76creates/stickers` (flex
  containers).
- Wires `github.com/lrstanley/bubblezone` for click regions —
  mouse-on by default at the `tea.NewProgram` level.
- Each pane is a hosted Tier 1 `Component` looked up by `command`
  in the registry.
- Tab / Shift+Tab cycles pane focus. Number keys (1-9) jump to a
  numbered pane. Click on a pane focuses it.
- Inter-pane routing uses the `OpenMsg{Target, ID}` / `LoadMsg{ID}`
  convention from [[feat-tui-component-interface]]. The
  `$selected_agent` template variable resolves against the
  `ui.state.<operator>.selected_agent` NATS KV key.
- Persistent status/help footer; `q` exits cleanly.

### 4. Tests + VHS

- Unit tests for the config loader (default vs. override vs.
  malformed TOML).
- Visual test: `tests/visual/dash_default.tape` exercises the
  canonical layout under VHS.

## Acceptance

- `sextant dash` opens a multi-pane TUI composing at least
  `agents` + `conversation` + `pending` per the default config.
- Tab switches pane focus; mouse click on a pane focuses it.
- Selecting an agent in the agents pane sends
  `OpenMsg{Target: "agent", ID: ...}`, the dash resolves it to the
  conversation pane, and the conversation pane loads the agent's
  frames.
- `~/.config/sextant/config.toml` overrides the embedded default
  when present.
- `sextant dash --dump-default-config` prints the embedded TOML.
- No code in `pkg/tui/chat/` or `pkg/tui/agents/` is dash-aware —
  they're hosted the same way standalone or in-dash.

## Why P2 (not P3)

`sextant dash` is the conventions doc's flagship surface; this is
the surface operators reach for first once they know it exists.
Promoting one tick above the sibling tickets to reflect that.

## Dependencies

- Hard-blocks on [[feat-cli-iflag-tier1-components]] — needs the
  registry to compose panes from.

## Out of scope

- Pane content beyond what the existing Tier 1 components provide
  (no new components introduced by this ticket).
- Custom NATS KV state schema beyond the `selected_agent` field
  the routing already needs.

## Related

- [[feat-cli-iflag-tier1-components]] — must land first.
- [[feat-sextant-tui-discovery]] — sibling.
- [[feat-tui-component-interface]] — interface this composes
  (resolved).
- [[feat-tui-theme-package]] — components share theme roles via
  this package (resolved).
- [[feat-tui-vhs-fixture-design-loop]] — VHS fixtures for testing.
- [[feat-cli-i-flag-tier1-tier2]] — original umbrella (resolved by
  this split).
