---
id: TASK-66
title: >-
  Resolve nilaway warnings in pkg/tui/chat (4 false positives masking
  pre-existing CI breakage)
status: Done
assignee: []
created_date: '2026-05-27 11:50'
labels:
  - chore
  - lint
  - ci
  - tech-debt
  - 'slug:chore-nilaway-tui-chat-false-positives'
  - P3
  - 'closed:fixed'
dependencies: []
priority: low
ordinal: 66000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Summary

`make lint-nilaway` exits 3 on `origin/main` and every branch with these four warnings:

```
pkg/tui/chat/turn.go:125:4: Potential nil panic detected.
  - chat/turn.go:125:4: unassigned variable `turns` sliced into

pkg/tui/chat/turn.go:174:6: Potential nil panic detected.
  - chat/turn.go:119:29: unassigned variable `turns` passed as arg `turns` to `lastAgentTurnIndex()`
  - chat/turn.go:174:6: function parameter `turns` sliced into

pkg/tui/chat/view.go:284:24: Potential nil panic detected.
  - chat/view.go:394:9: unassigned variable `out` returned from `wrapWithFirstWidth()` in position 0
  - chat/view.go:284:24: result 0 of `wrapWithFirstWidth()` sliced into via assignment to `msgChunks`

pkg/tui/chat/view.go:284:24: Potential nil panic detected.
  - chat/view.go:428:9: unassigned variable `out` returned from `wordWrap()` in position 0
  - chat/view.go:394:9: result 0 of `wordWrap()` returned via `wrapWithFirstWidth()` in position 0
  - chat/view.go:284:24: result 0 of `wrapWithFirstWidth()` sliced into via assignment to `msgChunks`
```

These look like false positives — Go slices that nilaway thinks could be nil but are always initialized via the surrounding control flow. Concretely:

- `turn.go:119` reads `turns := m.turns` from the Model; that field is initialized by `New()` to `nil` and then mutated by `WithTurns` / `frameMsg` handling. A nil `m.turns` is a valid state (empty agent), so the function should handle that case explicitly OR initialize to `make([]Turn, 0)` so nilaway sees the non-nil flow.
- `view.go:394` / `view.go:428` are inside `wrapWithFirstWidth` / `wordWrap`. The returned `out` slice goes through `append(out, ...)` patterns; nilaway can't track that `append` to a nil slice is fine in Go.

## CI history

Prior to phase-2 polish PR, CI's Go job ran:

```yaml
run: make lint-go lint-nilaway test-go
```

The `install golangci-lint` step ahead of this was failing on checksum verify, never reaching the make step. nilaway looked clean only because it was never actually invoked. Once `golangci/golangci-lint-action@v7` replaced the install + `lint-go` portion, the make step started running `lint-nilaway` for real and surfaced these warnings.

The phase-2 PR's CI temporarily drops `make lint-nilaway` from the workflow. This ticket restores the gate once the warnings are resolved.

## Fix shape

Pick whichever is cleanest per warning:

1. **Initialize slices explicitly** — change `var out []string` to `out := []string{}` at the top of `wordWrap` / `wrapWithFirstWidth`. nilaway treats the explicit empty slice as definitely-non-nil even though Go itself doesn't care.
2. **Add a nil-check on entry** to `lastAgentTurnIndex(turns []Turn)` — `if len(turns) == 0 { return -1 }` makes the slicing later unreachable for nil.
3. **Defensive sentinel** — pre-set `m.turns = []Turn{}` in `New()` so the field is never nil. (Slightly less efficient but eliminates the class of warning here.)

## Acceptance

- `make lint-nilaway` exits 0 on the branch + main.
- `.github/workflows/ci.yml` restores `make lint-nilaway` to the test-go step.
- No new false positives elsewhere — the chat warnings shouldn't migrate.

## Related

- `pkg/tui/chat/turn.go` — `lastAgentTurnIndex` + callers.
- `pkg/tui/chat/view.go` — `wordWrap` + `wrapWithFirstWidth`.
- `.github/workflows/ci.yml` — the disabled gate.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/chore-nilaway-tui-chat-false-positives.md
Discovered in: phase-2 polish PR CI green-up — the prior install-golangci-lint step had been silently short-circuiting `make lint-nilaway test-go`, so nilaway hadn't actually been gating PRs; once the install was fixed via the golangci-lint-action, nilaway's failures became visible
Original created_at: 2026-05-27T11:50-07:00
Fixed in: bc58b17
<!-- SECTION:NOTES:END -->
