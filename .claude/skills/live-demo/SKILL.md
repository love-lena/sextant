---
name: live-demo
description: >-
  Build a one-command, self-contained live demo script (demo.sh) that lets a
  reviewer validate a feature hands-on: throwaway infrastructure, a scripted
  counterpart, printed steps, and a self-validating epilogue. Use when a
  change needs human validation of real interactive behavior — "add a demo",
  "make this easy to review", "how do I show this works" — and the reviewer
  should drive it themselves. One of two demo media in this repo: for a
  non-interactive visual record (a gif in a PR or doc), use a VHS tape
  (cmd/*/testdata/*.tape) instead.
---

# Live demo scripts

A live demo is one command that drops the reviewer into the real feature with
everything staged, tells them exactly what to do and what they should see,
and proves the result from a second vantage point when they exit. The
reviewer asks no questions and reads no setup docs. Worked example:
[clients/claude-code/demo.sh](../../../clients/claude-code/demo.sh) (TASK-22).

## Choosing the medium

- **Live demo script** — the reviewer interacts: a session to drive, a TUI to
  click through, behavior to poke at. Validates that the thing *works*.
- **VHS tape** — the reviewer watches: a deterministic recording for a PR
  description or doc. Validates that the thing *looks right*. Tapes live in
  `cmd/<binary>/testdata/*.tape`.

Interactive validation → script. Visual record → tape. Both is common: tape
for the PR, script for the sign-off.

## Anatomy (all seven, in order)

1. **Self-contained workspace.** `mktemp -d` for all state; build binaries
   from the checkout into `$D/bin` (never assume installed versions); prepend
   to PATH for child processes only. `set -euo pipefail`.
2. **Real infrastructure, throwaway state.** Start the actual bus/server with
   a temp store and random port. Wait for readiness with a bounded poll on an
   observable artifact (e.g. the discovery file) — never `sleep` and hope.
3. **Scripted counterpart.** A background peer that makes the feature visibly
   *collaborative*: it reacts to the reviewer's actions. Poll with cursors
   (`read --since`), never stdout tail-pipes (they buffer and silently sit on
   events). **Bound it** — cap its replies so two well-mannered agents can't
   loop forever.
4. **Banner: steps + expectations.** Before handing off, print numbered
   steps — what to type, what to approve, and *what they should see* (the
   exact markers, e.g. "alice's reply injects as a ← sextant event"). A `say`
   helper with a colored `[demo]` prefix keeps script output distinct from
   feature output.
5. **Foreground hand-off.** Run the interactive surface in the staged
   environment. The reviewer drives; the script waits.
6. **Self-validating epilogue.** On exit, print independent evidence from the
   *other side* of the interaction — the peer's transcript, the directory,
   the state that proves the round-trip — so validation doesn't rely on what
   the reviewer remembers seeing.
7. **Cleanup + leave-behind notes.** `trap cleanup EXIT` for processes and
   temp state. Anything that intentionally persists (an installed plugin, a
   config entry) gets a printed removal one-liner. Setup that touches durable
   state must be idempotent (remove-then-add), so reruns just work.

## Skeleton

```bash
#!/usr/bin/env bash
# One-command demo of <feature> (TASK-NN). Header: what it stages,
# the reviewer's numbered steps, what exit prints.
set -euo pipefail
REPO="$(cd "$(dirname "$0")/../.." && pwd)"
D="$(mktemp -d /tmp/<feature>-demo.XXXXXX)"
say() { printf '\033[1;36m[demo]\033[0m %s\n' "$*"; }

say "building from $REPO" && mkdir -p "$D/bin"
(cd "$REPO" && go build -o "$D/bin/<binary>" ./cmd/<binary>)
# start infra; bounded readiness poll; register throwaway identities
# background counterpart (cursor poll, capped replies); PID for cleanup
trap 'kill "$PEER_PID" "$INFRA_PID" 2>/dev/null || true' EXIT

say "1. <first step>   2. <what to type>   3. <what you should see>"
(cd "$D/proj" && PATH="$D/bin:$PATH" <interactive surface>) || true

say "evidence from the other side:" && <peer-view transcript command>
say "state was under $D; persists: <thing> — remove with: <one-liner>"
```

## Quality bar (all three before "done")

- **Run it yourself in a PTY** (tmux: launch, drive every dialog and step,
  exit, read the epilogue). A demo that was never driven end-to-end is not
  done — unit-green scripts still die on launch-path and dialog-order bugs.
- **Hands-off runtime under ~3 minutes**, including the reviewer reading the
  banner. Trim staging, not evidence.
- **Zero outside knowledge**: every action the reviewer takes is in the
  banner; every success marker is named before it appears.
