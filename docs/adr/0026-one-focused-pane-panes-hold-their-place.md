---
status: proposed
date: 2026-06-09
---

# One focused pane; panes hold their place

The dash focuses exactly one pane at all times, and keys always go to the
focused pane. Moving focus never changes what a pane shows: a browser holding
an open conversation keeps holding it while the operator works elsewhere, the
way tmux panes hold their programs. There is no layout level to step in and
out of.

## The model

**Focus.** One pane is focused; every other pane is unfocused. The focused
pane receives all keys except the focus-movement and quit keys below. An
unfocused pane still renders its place — its cursor and detail stay visible,
muted.

**Moving focus** uses keys a surface never claims, so they work from any
state — mid-list, mid-conversation, mid-compose:

- `Tab` / `Shift+Tab` cycle through the visible panes.
- `Ctrl+h` / `Ctrl+j` / `Ctrl+k` / `Ctrl+l` move spatially (left / down /
  up / right), vim-style.

**Within the focused pane**, Enter and Esc are content navigation, exactly as
ADR-0024 shaped them: Enter opens the selected row's detail in place; Esc pops
the detail back to the list; Esc at the list does nothing. Detail state is
pane state — it persists until the operator pops it, not until focus leaves.

**Quitting.** `Ctrl+C` always quits. `q` quits from any pane that is not
capturing text; while a compose is capturing, `q` types a q.

All of these are keymap entries (`theme.Keymap`), overridable like every
other binding. `Ctrl+h` shares a byte with Backspace on legacy terminals
(modern terminals send DEL for Backspace, so the two are distinct keys); an
operator on a terminal with the legacy mapping rebinds spatial-left.

## Why

The cockpit is three browsers, each holding its own place — one in a DM, one
in a topic conversation, one on a document. Moving between them is the
dash's core gesture, and a focus model with a step-in/step-out level makes
that gesture cost an unwind: leaving a pane meant Esc'ing through its levels,
and the Esc that left also closed the detail. Pane content and pane focus are
independent concerns; the model now keeps them independent.

One level of focus is also one fewer mode to read: the keys always go where
the operator is looking, and the status bar names the focused pane rather
than a layout/pane mode split.

## What this replaces

The layout-level/pane-level split from ADR-0023 (selected vs active, Enter to
step in, the `DoneMsg` step-out intent) is gone. Surfaces keep the three-state
focus enum (`idle`/`selected`/`active`) for rendering — unfocused-but-visible
panes render the muted (`selected`) cue — but the layout no longer has a
resting level of its own. ADR-0024's "one Esc, one level" survives intact
inside the pane; it simply no longer governs leaving the pane, because
leaving is not a level.
