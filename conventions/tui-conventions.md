# TUI / CLI conventions — sextant

Sextant ships many small TUIs and a CLI. To feel cohesive without being rigid, every UI follows these conventions.

## Library to use

- **Go TUIs**: [Bubble Tea](https://github.com/charmbracelet/bubbletea) + [Lipgloss](https://github.com/charmbracelet/lipgloss). Bubbles for common components.
- **TS TUIs / UIs**: [Ink](https://github.com/vadimdemedes/ink) for terminal; whatever for web (TBD).
- **Client library**: `sextant-client-go` (Go) or `@sextant/client` (TS). Always.

## Package layout for library-style TUIs

When the TUI is a library consumed by a sextant subcommand (rather than a standalone binary), it lives under `pkg/tui/<surface>/` — `pkg/tui/chat/` is the precedent. Standalone TUI dev binaries continue to use `cmd/sextant-tui-<surface>/` (e.g. `cmd/sextant-tui-agents/`, `cmd/sextant-tui-chat-preview/`).

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

### `ui.state.*` key format

Bucket: `ui_state` (defined in `pkg/natsboot/layout.go`; one bucket holds every operator's keys).

Key string: dot-separated, lower-snake; the literal `ui.state` prefix is implicit in the bucket and **not** repeated in the key. The on-the-wire key shape is `<operator>.<field>`. For example:

- `lena.selected_agent`
- `lena.focused_pane`
- `lena.filter`

The legacy `ui.state.<operator>.<field>` spelling refers to the same key — read either way in prose. Code MUST write `<operator>.<field>`.

Value format (per field):

| Field | Value | Empty / unset semantics |
|---|---|---|
| `selected_agent` | RFC-4122 UUID string, ASCII, no quotes, no surrounding JSON | Absent key = no selection. A deliberate "no selection" write uses the literal value `none`. |
| `focused_pane` | Opaque ASCII string the producing TUI defines | Absent key = no focus |
| `filter` | Filter DSL expression (TBD; out of scope for M13) | Absent key = no filter |

**Operator identity**: TUIs source the operator name from, in order of precedence:

1. The `--operator <name>` CLI flag, when given.
2. The `SEXTANT_OPERATOR` environment variable, when set non-empty.
3. `os/user.Current().Username` (falling back to `USER` env var if that lookup fails).

The chosen name is sanitized to `[a-zA-Z0-9_-]+` — invalid chars are replaced with `_` — so it slots safely into a NATS KV key. Empty after sanitization is a startup error.

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

## Charm / Lipgloss patterns

The chat TUI buildout surfaced these patterns; they apply to any
Bubble Tea / Lipgloss work in the repo.

### Adaptive colors are the default

Every color must adapt to terminal background. Use
`lipgloss.AdaptiveColor{Light: "...", Dark: "..."}` — never a bare
`lipgloss.Color("237")` for anything operators will see.

Reasoning: `colMuted = "8"` is invisible on light terminals;
`colSelectBg = "237"` renders as near-black on light backgrounds. Bare
ANSI codes are theme-dependent and will look wrong half the time.

### Multi-line backgrounds break across inner resets

When you call `outerStyle.Background(bg).Render(content)`, lipgloss
wraps the content with `[bg]...[reset]`. But every inner `Render()`
call inside `content` emits its own `[reset]`, which clears the
background mid-line. The trailing whitespace looks unstyled even
though you set Background on the outer style.

Fix: propagate the background into every inner chunk AND every
literal-space separator. A helper pattern:

```go
func (m Model) withSelBg(s lipgloss.Style, selected bool) lipgloss.Style {
    if !selected { return s }
    return s.Background(m.styles.SelectedRow.GetBackground())
}
```

Apply to every styled piece on the row. Also wrap the inter-chunk
whitespace in a bg-styled span.

### `Style.Width(n)` on a bordered style sets OUTER width

`lipgloss.NewStyle().Border(...).Width(80).Render(content)` produces
an 80-cell wide box including the two border cells, not 80 cells of
content plus borders. Off-by-one bugs from this are common — verify
the math against rendered output, not your intuition.

### `bubbles/textarea` mode states are independent

Placeholder, prompt, focus, blur, and value are independently
controllable. Use this for NORMAL ↔ INSERT transitions: blur on Esc,
focus on `i`, keep the value across mode flips, swap the placeholder
on read-only variants.

### Word-wrap must preserve internal whitespace

The naive wrap pattern — split by `strings.Fields()`, rejoin with
single spaces — destroys ASCII art, code blocks, leading indents, and
column alignment. Operators will paste code into prompts and expect
the rendering to survive.

Default chat-content wrap: emit lines that fit verbatim (preserve all
whitespace); fall back to word-wrap only on lines that exceed the
width budget.

### Charm doesn't give you the design language automatically

Rounded borders, structured panes, breathing room, faint dividers —
all manual composition. `bubbles` provides components, not layout. A
sound mental model: bubbletea handles input loop + reducer; lipgloss
handles styling primitives; everything between (panes, dividers,
margins) is your code.

## TUI design surface conventions

### Compose-first default for chat-like surfaces

When a TUI has both a content stream and a composer (chat, prompt
review, anything operator-input-driven), default focus to the
composer, not the stream. Operators open these surfaces to type, not
to navigate. `k` (or equivalent) moves into the stream when they
actually want to navigate.

### Glue stream content to the bottom of its pane

When stream content fits within the viewport, top-pad with empty
lines so the content hugs the bottom edge. Filling from the top reads
as "empty surface"; bottom-glued content reads as "fewer messages so
far, more will arrive below."

### Restraint over decoration

Don't add visual elements that duplicate information already conveyed
by something else. `●  alice:` and `alice:` (with `alice` colored)
say the same thing — drop the glyph.

Limit hard-coded visual elements; let role-token styles carry the
distinctions. Every additional glyph or marker is operator visual
cost.

### Selection treatment: bar + tint, not glyph alone

For "this row is selected" in a list, use `lipgloss.BorderLeft` with a
single `▌` glyph PLUS a subtle background tint, applied to every line
of the selected block. The bar alone is easy to miss when content
wraps; the tint alone fights with text colors; together they read
unambiguously.

## Runnable mockups for visual design

Before merging a TUI feature, ship a runnable preview binary (under
`cmd/sextant-tui-<surface>-preview/`) that boots the model with a
seeded fixture transcript. This is the contract operators (and the
designer) iterate against.

Why: a markdown spec describes structure; structure is not the same
as design. The first render off a "complete" spec usually looks
incoherent until iterated against an eye. The preview binary makes
that iteration loop trivially fast — load fixture, render, look, tweak
style.go, re-run.

The preview can live alongside the package; promote it to `cmd/` when
it stabilizes (precedent: `cmd/sextant-tui-chat-preview/`).
