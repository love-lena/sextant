---
title: VHS design loop — remaining wiring (--fixture flag, agents_list + pending_list tapes, chat-preview migration)
status: resolved
priority: P3
created_at: 2026-05-26T23:55-07:00
resolved_at: 2026-05-27T04:30-07:00
labels: [feature, tui, testing, design-loop, follow-up]
discovered_in: feat-tui-vhs-fixture-design-loop landed in two passes — pkg/fixtures + Makefile target + first tape shipped on main; this ticket tracks what's left
---

## Resolution

The `cmd/sextant-tui-chat-preview/main.go` migration to `pkg/fixtures.Demo` landed (this commit) — the preview binary and `tests/visual/chat_default.tape` now share the same dataset via `fixtures.Get("demo")` + `fixtures.ChatFrames(...)`. The previous 70+ lines of inline bespoke fixture data are gone.

Remaining items from this ticket split off to their own follow-ups since they're each design-or-architecture-blocked rather than pure execution:

- **`--fixture <name>` flag on TUI-entry commands** — pairs with `[[feat-cli-output-protocol-tail-and-errors]]` (needs-input on tail's NDJSON shape) since both rewrite the same RunE bodies. Filed as part of that ticket's queue.
- **`tests/visual/agents_list.tape` + `pending_list.tape`** — gated on `[[feat-cli-i-flag-tier1-tier2]]` (needs-input) for `pending list -i`. Will land alongside that work.
- **`.github/workflows/screenshots.yml`** — optional per the original ticket; can ship anytime, no design dependency.

## Summary

`feat-tui-vhs-fixture-design-loop` landed the load-bearing pieces:

- `pkg/fixtures/` package (commit `9717abf`)
- `make screenshots` Makefile target + `tests/visual/chat_default.tape` (commit `532afbd`-ish)

What's still owed per the original ticket:

1. **`--fixture <name>` hidden flag** on TUI-entry commands (`sextant agents list -i`, `sextant agents chat`, `sextant pending list -i`, future `sextant dash`). When set, the command wires the in-memory `pkg/fixtures.Bus` instead of dialing the daemon. Best landed alongside the JSON envelope wave ([[feat-cli-output-protocol]]) since both rewrite the same RunE bodies.
2. **`tests/visual/agents_list.tape`** — capture for the `cmd/sextant-tui-agents` binary against the Demo fixture.
3. **`tests/visual/pending_list.tape`** — capture for `pending list -i` (after the `-i` flag lands per [[feat-cli-i-flag-tier1-tier2]]).
4. **Migrate `cmd/sextant-tui-chat-preview/main.go`** to consume `pkg/fixtures.Demo` via the `ChatFrames` adapter. Currently the preview binary carries 70+ lines of bespoke fixture data; deleting it in favor of the shared dataset is the convention's stated end state.
5. **(Optional)** `.github/workflows/screenshots.yml` — run `make screenshots` on PRs touching `pkg/tui/` and upload PNGs as artifacts.

## Fix shape

Sequenced after `[[feat-cli-output-protocol]]` lands (so the `--fixture` flag rides the same envelope sweep) and `[[feat-cli-i-flag-tier1-tier2]]` (so the `-i` flag exists on `pending list`).

For (4), it's a self-contained sweep that can land any time:

```go
// cmd/sextant-tui-chat-preview/main.go
import "github.com/love-lena/sextant/pkg/fixtures"

frames := fixtures.ChatFrames(fixtures.Demo, fixtures.DemoAliceUUID())
m := chat.New(chat.Options{
    AgentName: "alice", // or pull from fixtures.Demo.Agents[0].Name
    Branch:    "main",
    Read:      *read,
}).WithTurns(chat.FramesToTurns(frames))
```

## Acceptance

- `sextant agents list -i --fixture demo` opens the agents TUI against the in-memory Bus (no daemon).
- `sextant agents chat <uuid> --fixture demo` opens the chat TUI populated with the Demo transcript.
- `tests/visual/agents_list.tape` produces `screenshots/agents_list.png` via `make screenshots`.
- `tests/visual/pending_list.tape` produces `screenshots/pending_list.png` via `make screenshots`.
- `cmd/sextant-tui-chat-preview/main.go` imports `pkg/fixtures` and contains no bespoke `chat.Frame` literals.

## Related

- `[[feat-tui-vhs-fixture-design-loop]]` — parent ticket; this one finishes the remaining items.
- `[[feat-cli-output-protocol]]` — natural co-landing for the `--fixture` flag.
- `[[feat-cli-i-flag-tier1-tier2]]` — gates the `pending list -i` tape.
