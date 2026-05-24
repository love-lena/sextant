# TUI / CLI conventions — sextant

Sextant ships many small TUIs and a CLI. To feel cohesive without being rigid, every UI follows these conventions.

## Library to use

- **Go TUIs**: [Bubble Tea](https://github.com/charmbracelet/bubbletea) + [Lipgloss](https://github.com/charmbracelet/lipgloss). Bubbles for common components.
- **TS TUIs / UIs**: [Ink](https://github.com/vadimdemedes/ink) for terminal; whatever for web (TBD).
- **Client library**: `sextant-client-go` (Go) or `@sextant/client` (TS). Always.

## Keymap conventions

Every TUI uses these bindings consistently. Local TUIs can add bindings but **must not override** these:

| Key | Action |
|---|---|
| `q` or `Ctrl+C` | Quit |
| `?` | Show help overlay |
| `Esc` | Cancel current modal / close help |
| `j` / `↓` | Next item / scroll down |
| `k` / `↑` | Previous item / scroll up |
| `g` | Top |
| `G` | Bottom |
| `/` | Search (where applicable) |
| `n` / `N` | Next / previous match |
| `Enter` | Select / open |
| `Tab` | Next focus area |
| `Shift+Tab` | Previous focus area |
| `r` | Refresh / reload data |

## Status bar

Every TUI has a status bar at the bottom showing:

```
<context info>                            <pending count>  <key hints>
```

- **Context info** (left): what the TUI is showing, current filter, etc.
- **Pending count** (right): number of pending user-input requests (§4a) across all agents. Helps the operator never miss them.
- **Key hints** (far right): 2-4 most relevant keybindings for the current state.

## Layout

- Avoid full-screen modals when possible — prefer side panels or inline expansion.
- Lists scroll within their pane; the surrounding chrome stays visible.
- Use color sparingly. Sextant theme: 4 primary colors (success, warning, error, info) + neutral text. Defined in `pkg/theme/`.

## Shared state (`ui.state.*` NATS KV)

UIs coordinate state via shared NATS KV keys. **All keys are scoped per operator**:

- `ui.state.<operator>.selected_agent` — currently-selected agent UUID
- `ui.state.<operator>.focused_pane` — opaque string, TUI-specific
- `ui.state.<operator>.filter` — current filter expression

A TUI that cares about selected agent **subscribes** to `ui.state.<operator>.selected_agent` and updates its view when it changes. The TUI that owns selection **writes** to that key.

If a TUI is run standalone (no companion TUI sharing state), it reads from the KV at startup and writes back on change, but doesn't break if no one else is subscribed.

## CLI conventions

- Every command supports `--json` for scriptable output.
- Default output is human-readable, paginated with `less -FX` when interactive.
- Exit codes: 0 success, 1 user error (bad args), 2 system error.
- Long-running commands print status to stderr, results to stdout.
- `sextant <noun> <verb>` is the canonical shape: `sextant agents spawn`, `sextant audit query`.

## Help

- `?` in a TUI opens a modal listing all keybindings for the current state.
- `sextant <command> --help` in CLI prints command help.
- `sextant help <topic>` for longer guides (man-page style).

## Error display

- Errors in TUIs: red banner at the top, dismissable with `Esc`. Don't crash the TUI on errors.
- Errors in CLI: print to stderr with a clear summary, exit non-zero.

## Theme tokens

Define in `pkg/theme/`. Don't hardcode colors in TUI code:

```go
theme.Success     // green-ish
theme.Warning     // yellow-ish
theme.Error       // red-ish
theme.Info        // blue-ish
theme.TextPrimary // foreground default
theme.TextMuted   // dim text
theme.Border      // pane borders
theme.Highlight   // selection background
```

Operators may override via `~/.config/sextant/theme.toml`.
