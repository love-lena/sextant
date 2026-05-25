# TUIs

The framework — the `pkg/client` library plus the `ui.state.*` KV coordination pattern — is the product; TUIs are demonstrations. At this snapshot, one TUI ships: `sextant-tui-agents`.

## `sextant-tui-agents` — the M13 agent-list TUI

**Source**: `cmd/sextant-tui-agents/`.

### When to reach for this

- You want to see live agent status without running `sextant agents list` repeatedly.
- You're building a sibling TUI and want a reference for the `ui.state.<operator>.selected_agent` integration pattern.

### Launch

```bash
sextant-tui-agents [--config-dir DIR] [--operator NAME]
```

`--operator` sets the operator scope in the `ui_state` KV bucket. Defaults to `$SEXTANT_OPERATOR`, then `os/user.Current().Username` (`conventions/tui-conventions.md`). The name is sanitized to `[a-zA-Z0-9_-]+`.

### What you see

- A list of every non-archived agent with name, lifecycle, last-frame age, template.
- A bottom status bar with the pending-input request count.
- Cursor position highlighted; selection synced to `ui.state.<operator>.selected_agent` in KV when you press Enter.

### Key bindings

Defined at `cmd/sextant-tui-agents/model.go:157-192`. Conforms to `conventions/tui-conventions.md`:

| Key        | Action                                              |
|------------|-----------------------------------------------------|
| `j` / `↓`  | Cursor down                                         |
| `k` / `↑`  | Cursor up                                           |
| `g`        | Jump to top                                         |
| `G`        | Jump to bottom                                      |
| `r`        | Refresh (re-call `list_agents`)                     |
| `Enter`    | Write selected UUID to `ui.state.<operator>.selected_agent` |
| `?`        | Toggle help                                          |
| `Esc`      | Dismiss error / close help                          |
| `q` / `Ctrl+C` | Quit                                            |

### What it subscribes / publishes

- Subscribe `agents.*.lifecycle` — any transition triggers a `list_agents` refresh.
- Subscribe `user_input.requests.>` — increments the pending count.
- KV-watch `ui_state` bucket, key `<operator>.selected_agent` — react to a sibling TUI changing the selection.
- KV-put `ui_state.<operator>.selected_agent` — when the operator presses Enter.

### Internal messages

`cmd/sextant-tui-agents/model.go:29-36, 297-344`:

- `agentsLoadedMsg` — `list_agents` RPC returned.
- `lifecycleMsg` — agents.*.lifecycle envelope arrived; refetch.
- `pendingDeltaMsg` — input-request count changed.
- `selectedAgentMsg` — `ui.state.<operator>.selected_agent` changed externally.
- `kvPutDoneMsg` — our own KV put completed (error handling).

### Test coverage

- `cmd/sextant-tui-agents/model_test.go` — Bubble Tea model transitions.
- `cmd/sextant-tui-agents/integration_test.go` — end-to-end with a real NATS instance.

## Writing your own TUI

Pattern (per `conventions/tui-conventions.md`):

1. Use **Bubble Tea + Lipgloss** for Go, **Ink** for TS.
2. Build on `sextant-client-go` / `@sextant/client`. Don't talk to NATS directly.
3. Implement the standard keymap (`q`, `?`, `Esc`, `j`/`k`, `g`/`G`, `/`, `Enter`, `Tab`, `r`).
4. Status bar at the bottom with context info + pending count + 2-4 key hints.
5. Coordinate state with siblings via `ui.state.*` KV keys. Subscribe + write. A standalone-run TUI still reads/writes its own key — that's fine.
6. Don't hardcode colours; use the theme tokens (the snapshot has them defined at `pkg/theme/` per the conventions; verify before importing).

A TUI with selection-sync, lifecycle subscription, a refresh-on-key, and a pending counter is ≈ 150 lines on top of `pkg/client`.

## Not shipped yet

Forward-looking conventions describe a conversation viewer, a pending queue TUI, an audit browser, and a worktree-list TUI as the next bootstrapping shipments. They're not in this snapshot.
