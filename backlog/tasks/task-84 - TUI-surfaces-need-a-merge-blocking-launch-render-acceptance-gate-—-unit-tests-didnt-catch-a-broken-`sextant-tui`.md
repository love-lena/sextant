---
id: TASK-84
title: >-
  TUI surfaces need a merge-blocking launch/render acceptance gate — unit tests
  didn't catch a broken `sextant tui`
status: To Do
assignee: []
created_date: '2026-05-29 11:40'
labels:
  - feature
  - tui
  - ci
  - test
  - process
  - operator-experience
  - 'slug:feat-tui-launch-acceptance-gate'
  - P2
dependencies: []
priority: medium
ordinal: 84000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## What happened (no blame — SOP-forming)

The interactive-surfaces workstream (v0.4.0/v0.5.0, `plans/rfc-tui-workstream.md`)
shipped 9 components with full unit tests + green CI, but **nothing ever
ran a rendered surface**. Driving `sextant tui` in a tmux PTY afterward
found:

- **4 of 9 menu entries errored when selected** (chat / agent-detail /
  agents-context / traces) — the menu ran `sextant <Command> -i` with no
  positional, so arg-requiring surfaces hit `accepts 1 arg(s)` and chat
  hit `unknown shorthand flag 'i'`. Fixed in #47.
- **`q`/`esc` didn't quit** the menu despite the help text. Fixed in #47.

The unit tests passed the whole time: they exercised reducer (`Update`)
logic and `component.List()` membership, but **never the registry→cobra
launch seam or any rendered output**. That seam is exactly where the bug
lived. This is a real gap in our test SOP, not a one-off — without a gate
it recurs the next time someone adds a surface.

## Why it matters

`PRINCIPLES.md` §2 treats operator-facing surfaces as first-class. A menu
that errors on ~44% of its entries is a correctness bug that shipped *and
released*. "It compiles and the reducer tests pass" is not evidence the
operator-facing thing works — only running it is.

## Fix shape — two complementary gates

### Gate 1 (primary, cheap, deterministic, merge-blocking now)

**A registry↔cobra consistency test.** For every `component.List()`
entry, assemble the menu's launch argv (`launchArgs(meta, placeholder)`
from `cmd/sextant/tui.go`, using a placeholder for any `Arg`) and assert
the cobra command tree *accepts* it — `root.Find(args)` resolves, flag
parsing succeeds (catches the `-i` / `unknown shorthand` class), and
`cmd.ValidateArgs` passes (catches the `accepts 1 arg(s)` class). Do
**not** run RunE — no daemon needed. This is a plain Go test in
`cmd/sextant`, so it rides the existing `lint + test (Go)` CI job and is
**merge-blocking for free**. It deterministically catches the exact bug
class #47 fixed, for every current and future surface.

Acceptance for Gate 1:
- `cmd/sextant` test iterates `component.List()`; for each, builds the
  menu launch argv and asserts the cobra tree accepts it (resolve + flag
  parse + ValidateArgs), with a stub positional for `Arg != ""` and the
  `NoIFlag` rule honored.
- A deliberately-broken Meta (e.g. `Command: "agents show"` with no
  `Arg`) makes the test fail — proving it would have caught #47.

### Gate 2 (render smoke — heavier; merge-blocking if cheap enough, else nightly)

**Drive each surface in a PTY against an ephemeral daemon and assert it
renders without an error banner / panic.** Options:
- Go test using a pty lib (`github.com/creack/pty`) + a seeded test
  daemon (the `cmd/sextantd` integration harness already boots one):
  launch each `-i` surface, capture the first frame, assert no
  `Error:` / `panic` / empty-render. Runs in `go test` → merge-blocking.
- Or the existing **VHS** infra (`tests/visual/*.tape`, `make
  screenshots`): add a tape per surface **and** for the `sextant tui`
  menu→select→launch path; render in CI and diff against goldens. VHS
  also satisfies PRINCIPLES §3 (runnable mockups / visual evidence).
  Today only a few tapes exist (chat, agents_list, pending_list) and
  none cover the new surfaces or the menu.

Gate 2 catches render/launch-time failures Gate 1 can't (panics, empty
frames, mis-sized layouts). Start with whichever is cheap to wire; Gate 1
is the must-have.

## SOP change

The impl-plan template already prescribes a "manual smoke + screenshot"
step for `-i` PRs (e.g. `plans/feat-0.4.0-interactive-surfaces-impl.md`
Task 1.4) — it just wasn't enforced. Make it a checked requirement:

- Any PR adding/changing a TUI surface, the `sextant tui` menu, or
  `sextant dash` must include evidence of a PTY drive (the `verify`
  skill's output, or a VHS frame) — and must pass Gate 1.
- Add a one-liner to `conventions/tui-conventions.md` §Testing: "reducer
  tests are necessary but not sufficient; the launch path and a rendered
  frame must be exercised."

## Acceptance

- Gate 1 lands as a `cmd/sextant` Go test and is green on `main`; a
  mutation test (temporarily break a Meta) confirms it fails loudly.
- `conventions/tui-conventions.md` documents the launch-path + render
  requirement.
- (Gate 2) at least the `sextant tui` menu→launch path has a render/PTY
  smoke, in CI or a documented `make` target.

## Related

- #47 — the menu launch + quit fix this gate would have caught.
- `cmd/sextant/tui.go` — `launchArgs` / `execComponent` / `resolveArg`
  (the seam under test).
- `pkg/tui/component/registry.go` — `Meta.Arg` / `ArgKind` / `NoIFlag`.
- `plans/rfc-tui-workstream.md` §8 (Testing) — reducer + golden + VHS
  strategy this hardens.
- `[[feat-tui-vhs-fixture-design-loop]]` / `[[feat-tui-vhs-remaining]]` —
  the VHS infra Gate 2 would extend.
- Cosmetic follow-ups found in the same PTY session (file separately if
  picked up): `worktree list -i` BRANCH column double-truncates; the
  daemon log file contains duplicated lines (a daemon double-logging bug,
  not the TUI).
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-tui-launch-acceptance-gate.md
Discovered in: post-v0.5.0 — the TUI workstream shipped 9 surfaces on unit tests + green CI without anyone driving a rendered TUI; manually driving `sextant tui` in a PTY then found 4 of 9 menu entries errored on launch and q/esc didn't quit (fixed in #47)
Original created_at: 2026-05-29T11:40-07:00
<!-- SECTION:NOTES:END -->
