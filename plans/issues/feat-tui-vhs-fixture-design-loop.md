---
title: Wire VHS tapes + --fixture flag + make screenshots so agents can iterate visually
status: in-progress
priority: P3
created_at: 2026-05-26T20:33-07:00
labels: [feature, tui, testing, design-loop]
discovered_in: CLI/TUI conventions adoption
---

## Progress (2026-05-26)

- **Done (commit `9717abf`):** `pkg/fixtures/` package shipped — Demo dataset (agents + transcripts + pending) and an in-memory `Bus` that serves it through the same interface methods TUI components consume from `*client.Client`. Tests in `pkg/fixtures/fixture_test.go`.
- **Remaining:** `--fixture <name>` hidden flag wiring on TUI-entry commands (best landed under cobra migration); `tests/visual/*.tape` files; `make screenshots` Makefile target; migrate `cmd/sextant-tui-chat-preview/` to consume `pkg/fixtures/`; optional `.github/workflows/screenshots.yml`.

## Summary

`conventions/tui-conventions.md` (Testing → VHS + Runnable mockups)
makes the design loop load-bearing: headless agents can't iterate on
a TUI they can't see, and teatest text goldens don't reveal what a
render *looks* like.

The convention:

- Every component ships at least one `.tape` file in
  `tests/visual/<surface>.tape`.
- Tapes set fixed `Width`/`Height`, launch the binary in a known
  fixture state, screenshot, exit.
- `make screenshots` runs every tape and writes PNGs to
  `screenshots/`.
- Deterministic state comes from a hidden `--fixture <name>` flag
  that swaps in a fake `client.Client` with canned data.
- Fixture data lives in `pkg/fixtures/` and is reused at two
  layers: teatest wires it directly into a fake client; VHS tapes
  invoke it via `--fixture`. Same data, two entry points.
- VHS runs via `ghcr.io/charmbracelet/vhs` in CI so it doesn't
  require local ttyd/ffmpeg.

Current state:

- No `tests/visual/` directory.
- No `--fixture` flag on any command.
- No `pkg/fixtures/` package.
- `cmd/sextant-tui-chat-preview/main.go` exists as a fixture-driven
  preview binary (the runnable-mockup convention), but the fixture
  data is bespoke inside the preview binary rather than shared with
  teatest.
- No `make screenshots` target.

## Fix shape

1. Create `pkg/fixtures/` housing canned datasets keyed by name:

   ```go
   var Demo = Fixture{
       Agents: []sextantproto.AgentSummary{...},
       Conversations: map[uuid.UUID][]sextantproto.Frame{...},
       Pending: []sextantproto.UserInputRequest{...},
   }
   ```

   With a `client.Client` factory that returns an in-memory fake
   backed by the fixture.

2. Add `--fixture <name>` (hidden) to every TUI-entry command
   (`agents list -i`, `conversation`, `pending list -i`, future
   `dash`). When set, the command wires the in-memory client
   instead of dialing the daemon.

3. Migrate `cmd/sextant-tui-chat-preview/` to consume the shared
   `pkg/fixtures/` data so the preview binary and VHS tapes draw
   from the same source. (Preview binary stays — it's the
   operator-driven iteration loop; VHS is the automated capture.)

4. Add `tests/visual/<surface>.tape` files. Start with:
   - `tests/visual/chat_default.tape`
   - `tests/visual/agents_list.tape`
   - `tests/visual/pending_list.tape`

5. Add `make screenshots` target:

   ```make
   screenshots:
   	docker run --rm -v "$(PWD)":/vhs ghcr.io/charmbracelet/vhs \
   		sh -c 'for tape in tests/visual/*.tape; do vhs "$$tape"; done'
   ```

6. Add `.github/workflows/screenshots.yml` (optional but tracked
   here): on PRs touching `pkg/tui/`, run `make screenshots` and
   upload PNGs as workflow artifacts.

7. Document the design loop in
   `conventions/tui-conventions.md` already — this ticket implements
   it.

## Dependencies

Best landed alongside [[feat-tui-component-interface]]: the fake
`client.Client` injection point the components use is the same one
the fixtures supply, so the two ship clean together.

## Acceptance

- `pkg/fixtures/demo.go` exists and exposes at least one canned
  fixture covering agents + conversations + pending.
- `sextant agents list --fixture demo` runs without a live daemon
  and prints the canned agents.
- `tests/visual/agents_list.tape` produces a PNG via `make
  screenshots`.
- The chat-preview binary uses `pkg/fixtures/` data; deleting the
  bespoke fixture in the preview compiles.
- An agent can run `make screenshots` and view the resulting PNGs
  to iterate on visual changes.

## Related

- `conventions/tui-conventions.md` § "Testing → VHS / Runnable
  mockups"
- [[feat-tui-component-interface]]
- `cmd/sextant-tui-chat-preview/` (current single-binary precedent)
