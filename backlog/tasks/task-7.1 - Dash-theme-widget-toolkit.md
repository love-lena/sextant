---
id: TASK-7.1
title: 'Dash: theme + widget toolkit'
status: To Do
assignee: []
created_date: '2026-06-06 02:59'
labels: []
milestone: 'M4: The dash (human UI)'
dependencies: []
references:
  - docs/adr/0023-the-dash-is-a-composable-pane-cockpit.md
  - docs/adr/0014-the-tui-is-a-client.md
parent_task_id: TASK-7
priority: medium
ordinal: 30000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Foundation of the dash library (ADR-0023, ADR-0014), no SDK: a theme package (base16 palette + role-hue tokens + status-by-shape glyphs + the locked keybinding set; light + dark) and the generic Bubble Tea widgets (cursor list, stream viewport, detail pane) that render only from theme tokens. Salvage target from TASK-14.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 theme: stock base16 light + dark + `Auto()` default; role/kind hue tokens; status glyphs; `DefaultKeymap()` + a working user-override path
- [ ] #2 widgets (cursor list, stream viewport, detail pane) render only from theme tokens (incl. the idle/selected/active 3-state focus cue), no SDK import
- [ ] #3 teatest goldens + a committed VHS `.tape` + a preview binary, PTY-verified; the rendered `.gif` attached to the PR for review
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
**Brief (resolved design — self-contained).**

Packages: `pkg/tui/theme` (palette, role/kind hue maps, glyphs, keymap) and
`pkg/tui/widget` (cursor list · stream viewport · detail pane + the rounded `box`
chrome). Import lipgloss/bubbletea + theme only — **no `pkg/sextant`, no `internal/`**
(an import check is part of done).

LOCKED from the `proto/dash-tui` prototype (reference; salvage it for this ticket, then
delete the dir):
- palette = **stock base16 "default" light + dark, no hand-tuning**; `Auto()` (termenv
  bg-detect → dark fallback) is the default; `--theme`/config overrides.
- role→hue: human=blue · coordinator=magenta · dispatcher=orange · agent=green ·
  system=grey (onto base16 accent slots); message *kind* (the verb) tinted separately.
- status by shape: `●` connected · `◔` idle · `⊘` draining.
- panel chrome: superfile/btop rounded frame, coloured title chip in the top border,
  per-segment colouring (never splice ANSI into already-styled text).

Interaction model (Lena 2026-06-08 — a *default*, expected to churn):
- **Two-level focus.** Layout level: arrows/hjkl move the selected pane; `Enter` steps
  in. Pane level: arrows/hjkl navigate within; `Esc` steps out.
- `o` = universal options menu (toggle panes · presets · theme · keys). `q` quits
  (`Ctrl-C` hard quit); `Esc` only steps out a level.
- Conversation = step-in-to-type: typing composes (no `c` key), `Enter` sends, `Esc` out.
- **Keys are overridable defaults, not a contract** → keymap as data: `DefaultKeymap()`
  of `bubbles/key.Binding`s + a user-override merge (rides 7.4's config). Nothing
  hardcodes a key; widgets read bindings from the keymap.
- Widget focus is **3-state**: idle / selected (accent border) / active (stepped in).

Verify (AC = `.tape` + goldens): teatest goldens (focused/unfocused, narrow/wide reflow,
empty/overflow) + a committed VHS `.tape`, preview binary driven in tmux; the rendered
`.gif` goes in the PR — **Lena reviews the look there**.
<!-- SECTION:NOTES:END -->
