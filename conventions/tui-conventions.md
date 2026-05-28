# CLI / TUI conventions — sextant

This document codifies how `sextant` presents itself: how commands are
structured, how TUIs relate to those commands, and what the interface
looks like. It is opinionated by design. Deviations need a reason.

The first half is the strategic spec (invariants, three tiers,
visuals, testing). The second half is sextant-specific empirical
guidance (shared NATS-KV state, operator identity, lipgloss patterns
we hit the hard way). Read both before touching an operator surface.

## Invariant

**Every TUI is an augmentation of a CLI path that works without it.**
Small TUIs wrap commands you could also run plain. The dash composes
those same commands as panes. There is no TUI-only functionality, ever
— if something can only be done in `sextant dash`, that's a bug.

The invariant is about primitives, not 1:1 mappings. A multi-step TUI
flow may correspond to a sequence of CLI calls. What it can't do is
reach into the daemon through a path that has no CLI equivalent. The
daemon API is the floor; the CLI is the first surface on top of it;
TUIs are built on the CLI's surface.

## Architecture: three tiers

**Tier 0 — CLI base.** Every command. Plain output, scriptable, no
interaction.

**Tier 1 — Component TUIs.** Opt-in interactive screens attached to
CLI commands via `-i`. Single-purpose, embeddable.

**Tier 2 — `sextant dash`.** Flagship multi-pane TUI. Composes Tier 1
components. Mouse on, full theming, the works.

Tier 0 is mandatory; Tiers 1 and 2 are built on top of it. A command
without a Tier 0 surface cannot have a Tier 1 surface.

## Tier 0: CLI base

### Framework

- **Cobra + Fang.** Fang styles help, errors, and version output.
- **`charmbracelet/log`** with two loggers: user-facing (default) and
  diagnostic (`-v`). Separate sinks, separate formatters.
- **Huh** for one-shot prompts in CLI flows (confirms, single inputs).
  Not for anything that belongs inside a Bubble Tea program.

Existing commands in `cmd/sextant/` are still on stdlib `flag` + the
custom `output.go` helpers. Migration is tracked under
`plans/issues/feat-cli-cobra-fang-migration.md`. New commands should
land on the target stack.

### Output discipline

- TTY detection at the boundary. Pretty when stdout is a terminal,
  plain when piped. Respect `NO_COLOR` and `--no-color`.
- **stdout = data, stderr = messages, never mixed.**
- No interactive prompts unless `-i` is set. A command run in a
  pipeline must never block waiting for input. If it needs info it
  doesn't have, fail with a clear message naming the missing flag.

### Command design

- **Resource-verb-modifier, in that order.** `sextant agents list`,
  not `sextant list agents`. Verbs cluster under resources; matches
  the daemon shape and makes tab completion useful.
  - **Documented exceptions:** `sextant init`, `sextant doctor`,
    `sextant version` are verbs on the **sextant install itself**
    (initialize, diagnose, identify the install) — folding them under
    a `sextant install <verb>` noun would add an explicit noun for a
    single-instance resource and buy nothing. Rationale tracked at
    `plans/issues/feat-cli-resource-verb-cleanup.md`.
- **Closed-exception verb vocabulary.** Default verbs:
  `list`, `show`, `create`, `update`, `delete`, `run`. New verbs
  should land as flags or subcommands of `update`, not top-level —
  the bar is "this maps to a first-class operator concept that wouldn't
  be visible if collapsed into `update`."
  - **Approved exceptions (closed list):** `restart`, `archive`,
    `prompt`, `answer`, `defer`, `escalate`, `tail`, `merge`, `diff`,
    `context`. Each justified by operator clarity that `update --kind=X`
    would erase. (`context` joined per
    `plans/issues/feat-agents-context-view.md` — inspecting the
    agent's prompt buffer is a first-class operator concept distinct
    from reading the agent record.) Adding an eleventh exception
    requires a needs-input ticket and Lena's call.
  - **Deprecated aliases (one-release backwards compat, removal in v0.2):**
    `agents spawn` → `agents create`; `agents kill` → `agents stop`;
    `audit query` → `audit list`; `worktree destroy` → `worktree delete`.
    The old spellings continue to resolve via cobra `Aliases`. Scripts
    that use them still work; the help text flags the alias and its
    deprecation horizon.
  - **Wire-protocol RPC verbs are unaffected.** This is a CLI-surface
    rename only — `spawn_agent`, `kill_agent`, `query_audit`,
    `worktree_destroy` keep their names on NATS subjects.
- **Read from stdin when natural.** `sextant agents show` accepts an
  ID as positional arg or reads IDs from stdin one per line.
  Positional arg wins; stdin is fallback. Enables `sextant agents
  list --json | jq -r '.data[].id' | sextant agents show`.
- **Exit codes carry meaning.** 0 success, 1 generic error, 2 usage
  error (bad flags), 10 = no results found (distinct from real errors
  so shell loops can branch on it).
- **Destructive ops need `--dry-run` and confirmation.** TTY: confirm
  via Huh. Non-TTY: require `--yes`. `--dry-run` prints what would
  happen, exits 0.
- **Pagination is explicit.** `--limit`, `--cursor`. Default limit
  small enough that piping to `less` isn't required. `--all` for
  everything.
- **Filter flags compose as AND.** `sextant decision list --agent foo
  --status pending` is conjunction. OR is `--json | jq` territory;
  don't build a query DSL into flags.

### JSON contract

Every command that produces data supports `--json`. Output is wrapped
in an envelope:

```json
{
  "data": [...],
  "meta": {"version": 1, "command": "queue list"}
}
```

Errors get an envelope too, to stderr, non-zero exit:

```json
{"error": {"code": "AGENT_NOT_FOUND", "message": "no agent with id xyz"}}
```

Schema rules: fields can be added, never removed or renamed; types
don't change; enums grow but don't reorder. Breaking changes bump
`meta.version`. Error codes are stable; messages are human and can
change.

## Tier 1: Component TUIs

### Entry

`-i` (or `--tui`) flag on the relevant command. Arg co-location wins:
`sextant review <id> -i` keeps the ID with the command.

`sextant tui` (no arg) is a Huh-driven listing command that enumerates
available interactive surfaces and launches the selected one.
Discovery without forking the command tree.

There is no `sextant tui <name>` direct path. Listing is for
discovery; the real entry is `-i` on each command.

### Component contract

Components implement a small interface beyond `tea.Model`:

```go
type Component interface {
    tea.Model // Init, Update, View

    SetSize(w, h int)
    Focus() tea.Cmd
    Blur()
    Focused() bool

    ShortHelp() []key.Binding
    FullHelp() [][]key.Binding
}
```

Conventions that go with the interface:

- **Chrome lives outside the component.** Titles, borders, status are
  drawn by the host (standalone wrapper or dash). The component
  renders its content area and nothing else. `SetSize` is the content
  rect.
- **Components don't quit the program.** They emit intent messages
  (`DoneMsg{}`, `OpenMsg{Target, ID}`) and the host decides whether
  to call `tea.Quit`, switch focus, or open a pane.
- **Components don't touch storage or business logic.** They hold a
  `client.Client` interface (the daemon wrapper at `pkg/client/`)
  injected at construction. Production wires the real client; tests
  wire a fake. CLI commands and components use the same client.

A component must be runnable standalone *and* mountable in the dash
with no code changes. Same `tea.Model`, different host.

### Package layout

Component TUIs live under `pkg/tui/<surface>/` — `pkg/tui/chat/` is
the precedent. Standalone TUI dev binaries continue to use
`cmd/sextant-tui-<surface>/` for whole-binary experiments (e.g.
`cmd/sextant-tui-agents/`). Per-component preview binaries that boot
the model against a seeded fixture transcript live at
`cmd/sextant-tui-<surface>-preview/` (precedent:
`cmd/sextant-tui-chat-preview/`) — see Testing → Runnable mockups
below.

### Cross-component routing

Components don't address each other. They emit intents:

```go
return m, func() tea.Msg {
    return OpenMsg{Target: "agent", ID: selected.ID}
}
```

The dash routes:

```go
case OpenMsg:
    pane := m.router.resolve(msg.Target)
    var cmd tea.Cmd
    m.panes[pane], cmd = m.panes[pane].Update(LoadMsg{ID: msg.ID})
    m.focus = pane
    return m, cmd
```

The receiving component handles `LoadMsg` the same way whether it
came from a sibling pane or from `sextant agents show <id>` at
startup — the standalone wrapper fires one `LoadMsg` in `Init`. Same
code path.

Focus follows explicit user actions (enter) by default; passive
selection doesn't move focus. Router decision, not component
decision.

Broadcast/subscription (multiple panes following one another's state)
is a v2 problem. Direct intent routing handles the common case.

### Action registry

Every component declares actions under its own namespace: `agent.show`,
`agent.list`, `review.approve`, `queue.next`. Conflicts impossible by
construction. Breaking changes inside a namespace are a
search-and-replace.

The registry is a flat `namespace.action → handler` map. The command
palette (v2) reads from it directly.

### Daemon as the floor

The daemon's API is the source of truth. The ordering is
non-negotiable:

1. Add the capability to the daemon.
2. Expose it as a CLI command.
3. Build the TUI on top.

If a TUI flow needs something the daemon doesn't expose, the daemon
gets it first. This is what guarantees the invariant.

There is a thin `pkg/client/` wrapper around the generated protobuf
client. Components and CLI commands depend on the wrapper, not on
`pb.SafetyServiceClient` directly. Keeps gRPC stubs out of the UI
layer and makes fakes straightforward.

## Tier 2: `sextant dash`

- **Stickers** for flex layout.
- **BubbleZone** for click regions. Mouse on by default.
- Composes Tier 1 components as panes. Doesn't reimplement them.
- Multi-pane routing via the message convention above.
- Explicit focus model; persistent status/help footer.
- Tier 1 keybinds inherited; dash-level adds pane switching and (v2)
  the command palette.
- One canonical spinner, one canonical progress style. Defined in
  `pkg/theme/`, used everywhere.
- No `--headless` mode. Per the invariant, everything dash-shaped is
  already a CLI command.

## Theme system

### Source of truth

Sextant owns its config. `$XDG_CONFIG_HOME/sextant/config.toml` and
`$XDG_CONFIG_HOME/sextant/themes/*.yaml`. No dependency on tinty.

The theme file format is base16-compatible YAML, because that's a
standard and makes it trivial to bring over terminal themes. `sextant
theme import <path>` copies a base16 file into the sextant themes
dir.

Config precedence: flag > `SEXTANT_*` env > config file > defaults.

### Tokens and roles

The palette is base16-shaped (`base00`–`base0F`). The app never reads
palette slots directly. Instead, base16 slots map to semantic role
tokens:

- `bg`, `bg_alt`
- `fg`, `fg_muted`
- `accent` (selection, focus, the one signal color)
- `danger`, `warning`, `success`
- `border`, `border_active`

That's the vocabulary. Every Lip Gloss style reads roles. If you find
yourself reaching for a seventh signal color, push back.

`lipgloss.AdaptiveColor` handles light/dark fallback when no theme is
loaded. `termenv` handles profile downgrades. `NO_COLOR` is
respected.

## Visual design language

The reference points are superfile and btop. Shared DNA: rounded
borders always, titles anchored to the border, status embedded in
border edges, active panes signaled by border color, tight density,
no decorative whitespace.

### Borders

- `lipgloss.RoundedBorder()` everywhere. No square corners.
- Title anchored to the top border, left-aligned, with an icon. Title
  sits inside the border, not floating above the pane.
- Status/count info embedded in border edges (top-right,
  bottom-right) where it fits. Optional per-pane.
- Numbered tab shortcuts on the top border for panes that have tabs
  (btop-style: `¹cpu ²mem ³disks`). The numbers are real keybinds.

### Active vs inactive

- Active pane: `border_active` color + brighter title.
- Inactive: `border` color, muted title.
- Standalone components are always active.
- Same border style (rounded) in both states. Color is the only
  signal.

### Color

The role vocabulary splits into two kinds:

- **Structural**: `bg`, `bg_alt`, `fg`, `fg_muted`, `border`,
  `border_active`. Carry the chrome.
- **Signal**: `accent`, `danger`, `warning`, `success`. Carry
  meaning.

Signal use is sparing. `accent` appears for one thing per screen —
the selection, the focus, the active state — never decoratively.
`danger`/`warning`/`success` appear only when there's real state to
communicate. If a screen needs a fifth signal color, the design is
wrong.

### Density

Single density, matching superfile. One column of internal padding
(1 char). Single-line list items. Section breaks get one blank line,
not two.

A comfy/compact toggle is easier to add later than to retrofit, so
we're shipping one density.

### Icons

Nerd Font icons before list items where they carry information (file
types, agent status, decision state). Never decorative.

Nerd Font is the default and the recommended setup — that's what the
design targets. But an ASCII fallback must remain *functionally
usable*: no missing information, no broken layout, no commands that
don't work. It is allowed to look ugly. The aesthetic floor is "this
works"; the aesthetic ceiling is what Nerd Font delivers.

Toggle via `config.icons = "nerd" | "ascii"`. The icon column is
always reserved in the layout so toggling doesn't shift content.
Every icon has both a Nerd Font glyph and an ASCII equivalent
declared in one place (the theme package); reaching for a new icon
means adding both.

### Selection

A selected row gets a left-border indicator (`lipgloss.BorderLeft`,
single `▌` glyph) **plus** a subtle background tint applied to every
line of the selected block. The bar alone is easy to miss when
content wraps; the tint alone fights with text colors; together they
read unambiguously.

The bar is a fixed-width border indicator — it doesn't shift content.
Never an inline glyph prefix (`>`, `*`, `→`) that does shift content.
Reverse video / background fill alone is acceptable for single-line
selections.

### Search and filter

Top of the pane, magnifier icon, single-line input. Inline with the
content area, not a modal. Activated by `/`. Placeholder text matches
superfile's style (`(/) Type something` or similar).

### Modals

Avoid when possible. When unavoidable (destructive confirm,
multi-field input), Huh handles one-shot prompts in CLI flows;
in-dash modals are centered, bordered the same way as panes, with
the background dimmed.

### Typography

Monospace everything. Titles in regular weight, not bold — the border
carries the emphasis. Italics reserved for placeholder and hint text.

### Empty states

Centered in the content area, muted text, one line of hint about
what to do next ("no agents — try `sextant agents create`"). Never
blank.

### Status bar

Every TUI has a status bar at the bottom showing:

```
<context info>                            <pending count>  <key hints>
```

- **Context info** (left): what the TUI is showing, current filter,
  etc.
- **Pending count** (right): number of pending user-input requests
  across all agents (sextant-specific — surfaces the §4a pending
  queue so operators never miss them).
- **Key hints** (far right): 2–4 most relevant keybindings for the
  current state.

## Keybindings

`bubbles/key` for bindings, `bubbles/help` for the help bar.
Canonical conventions, locked early. Local TUIs can add bindings but
**must not override** these:

| Key | Action |
|---|---|
| `j` / `↓` | Next item / scroll down |
| `k` / `↑` | Previous item / scroll up |
| `h` / `l` | Horizontal nav where applicable |
| `g` | Top |
| `G` | Bottom |
| `?` | Toggle full help |
| `/` | Open search |
| `n` / `N` | Next / previous search match |
| `Esc` | Cancel / back out / close modal |
| `q` | Quit (standalone) or close pane (dash) |
| `Ctrl+C` | Quit |
| `Enter` | Activate selected item |
| `Tab` / `Shift+Tab` | Next / previous focus area |
| `1`…`9` | Jump to that numbered tab within a pane |
| `r` | Refresh / reload data |

Dash adds:

- `Tab` / `Shift+Tab` cycles pane focus.
- Mouse click on a BubbleZone targets that pane/element.

## Logging, output, and rich text

- **`charmbracelet/log`** with two loggers (user-facing, diagnostic).
- **Glamour** for any markdown-flavored content: long help, error
  context, embedded docs.
- **`bubbles/spinner`** for indeterminate work;
  **`bubbles/progress`** for determinate. One canonical instance of
  each, defined in `pkg/theme/`.

### Error display

- Errors in TUIs: red banner at the top, dismissable with `Esc`.
  Don't crash the TUI on errors.
- Errors in CLI: print to stderr with a clear summary, exit non-zero.
- Every error the operator sees should be answerable by a
  copy-pasteable next step. `ask: agent has lifecycle=ended; restart
  with sextant agents restart X` beats `ask: timeout (waited 10s)`.
  See `conventions/operator-experience.md` for the full structured-
  remedy rule.

### Help

- `?` in a TUI opens a modal listing all keybindings for the current
  state.
- `sextant <command> --help` in CLI prints command help (Fang-styled).
- `sextant help <topic>` for longer guides (man-page style).

## Testing

Two complementary mechanisms, doing different jobs.

### `teatest` — text-level snapshots

Fast, deterministic, runs in CI on every commit. Tests model behavior
and rendered output as text.

- Every component has at least one golden test covering its default
  render at a fixed size.
- Components are tested standalone, with a fake `client.Client`.
- The dash gets its own integration tests covering routing and focus
  transitions, using fakes for all mounted components.

Golden files live next to the component
(`testdata/golden/<name>.txt`). Updating them is a deliberate act —
`go test -update` or equivalent, reviewed in the diff.

### VHS — visual screenshots for design review

`charmbracelet/vhs` runs the binary in a headless terminal and
captures PNGs via the `Screenshot` tape command. Slower than
teatest, opt-in. The point is to produce images that a human *or an
AI agent* can look at and react to.

This is load-bearing for the design loop. An agent can't iterate on
a TUI it can't see; teatest's text snapshots tell you whether the
model produces the right characters, not whether the result looks
right. PNGs close that gap.

The convention:

- Every component ships with at least one `.tape` file in
  `tests/visual/`.
- The tape sets a fixed `Width`/`Height`, invokes the binary in a
  deterministic state, captures a screenshot, and exits.
- The dash gets its own tapes for the canonical layouts (default
  panes, focus states, modal open, search active).
- `make screenshots` runs all tapes and writes PNGs to
  `screenshots/`. An agent (or human) can then `view` them.

Deterministic state means the binary needs a way to be launched with
a known fixture. The convention is a `--fixture <name>` flag (hidden
from normal help) that swaps in a fake `client.Client` with canned
data. Fixture data lives in `pkg/fixtures/` and is reused at two
layers: teatest wires it directly into a fake client; VHS tapes and
manual runs invoke it via `--fixture`. Same data, two entry points.

Example tape (`tests/visual/agents_list.tape`):

```
Output screenshots/agents_list.png

Set Width 1200
Set Height 600
Set FontSize 16

Type "sextant agents list -i --fixture demo"
Enter
Sleep 1s
Screenshot screenshots/agents_list.png
```

The tape doesn't need to drive the TUI through interactions for the
basic case — just launch, settle, screenshot. Tapes that exercise
interactions (opening a modal, navigating, selecting) capture
multiple screenshots in one run.

Run VHS via the official Docker image
(`ghcr.io/charmbracelet/vhs`) in CI so it doesn't require local
installation of ttyd/ffmpeg. Locally, a `make screenshots` target
wraps the Docker invocation.

### Freeze — static captures for docs

`charmbracelet/freeze` renders ANSI output (or code) to polished
PNG/SVG images with optional window chrome. Use it for README assets
and design-doc captures of CLI commands:

```
sextant agents list --fixture demo | freeze --output docs/agents-list.png
```

Freeze is non-interactive — it renders whatever output it's given;
it doesn't drive a TUI through key events. The split is:

- **VHS** for anything interactive. Tape-driven, captures TUI
  states, design iteration loop.
- **Freeze** for static CLI captures. One-shot, polished, faster
  than a tape for a single image. SVG output when scaling matters
  (READMEs viewed at multiple sizes).

If you find yourself writing a VHS tape that just launches a command
and screenshots — no interaction — that's Freeze territory.

### Runnable mockups — preview binaries

Before merging a TUI feature, ship a runnable preview binary (at
`cmd/sextant-tui-<surface>-preview/`) that boots the model with a
seeded fixture transcript. This is the contract operators (and the
designer) iterate against. Precedent:
`cmd/sextant-tui-chat-preview/`.

Why: a markdown spec describes structure; structure is not the same
as design. The first render off a "complete" spec usually looks
incoherent until iterated against an eye. The preview binary makes
that iteration loop trivially fast — load fixture, render, look,
tweak `style.go`, re-run.

Preview binaries and VHS tapes share the same fixture data
(`pkg/fixtures/`): the preview is the operator-driven iteration
harness, the tape is the automated capture for headless review. The
preview can also be promoted to a standalone dev tool when it
stabilizes.

### What this enables

A headless agent working on a sextant component can:

1. Make a code change.
2. Run `make screenshots` (or the targeted tape).
3. View the resulting PNG.
4. Iterate.

This is the design loop sextant is built to support. Components
without a tape file are incomplete.

## Long-running operations and streaming

Standardized message types:

- `LoadingMsg{}` — component shows a spinner.
- `LoadedMsg{Result T}` — component renders the result.
- `ErrorMsg{err}` — host decides how to surface (toast in dash,
  inline in standalone).

Streaming endpoints (decision queues, log tails) use a
channel-backed `tea.Cmd` that re-yields on each chunk. One canonical
pattern, one example in the codebase, copied for each new streaming
surface.

## Open items

Deferred until there's enough surface to design them well:

- **Command palette.** Requires a stable action namespace vocabulary
  across multiple components. Action registry is in place; palette
  UI lands when the dash has 3–4 panes.
- **Error surfacing convention in the dash.** Transient toast vs
  inline vs modal. Components emit `ErrorMsg`; the dash will need a
  small surface for rendering them. Decision lands after the second
  pane is built.
- **Broadcast/subscription routing.** Multi-pane state following
  (context flipper-style). Direct intent routing handles v1; this
  is v2.
- **Comfy/compact density toggle.** Single density now; toggle later
  if feedback warrants it.

---

# Sextant-specific guidance

The sections above are project-agnostic. What follows is sextant-
specific: shared state on the bus, operator-identity selection, and
empirical lipgloss patterns this codebase paid for in real
debugging time. Drop these into any merge of this doc with caution
— they encode lessons, not aesthetic preference.

## Shared state (`ui.state.*` NATS KV)

UIs coordinate state via shared NATS KV keys. **All keys are scoped
per operator**:

- `ui.state.<operator>.selected_agent` — currently-selected agent
  UUID.
- `ui.state.<operator>.focused_pane` — opaque string, TUI-specific.
- `ui.state.<operator>.filter` — current filter expression.

A TUI that cares about selected agent **subscribes** to
`ui.state.<operator>.selected_agent` and updates its view when it
changes. The TUI that owns selection **writes** to that key.

If a TUI is run standalone (no companion TUI sharing state), it
reads from the KV at startup and writes back on change, but doesn't
break if no one else is subscribed.

### `ui.state.*` key format

Bucket: `ui_state` (defined in `pkg/natsboot/layout.go`; one bucket
holds every operator's keys).

Key string: dot-separated, lower-snake; the literal `ui.state`
prefix is implicit in the bucket and **not** repeated in the key.
The on-the-wire key shape is `<operator>.<field>`. For example:

- `lena.selected_agent`
- `lena.focused_pane`
- `lena.filter`

The legacy `ui.state.<operator>.<field>` spelling refers to the same
key — read either way in prose. Code MUST write `<operator>.<field>`.

Value format (per field):

| Field | Value | Empty / unset semantics |
|---|---|---|
| `selected_agent` | RFC-4122 UUID string, ASCII, no quotes, no surrounding JSON | Absent key = no selection. A deliberate "no selection" write uses the literal value `none`. |
| `focused_pane` | Opaque ASCII string the producing TUI defines | Absent key = no focus |
| `filter` | Filter DSL expression (TBD; out of scope for M13) | Absent key = no filter |

## Operator identity

TUIs source the operator name from, in order of precedence:

1. The `--operator <name>` CLI flag, when given.
2. The `SEXTANT_OPERATOR` environment variable, when set non-empty.
3. `os/user.Current().Username` (falling back to `USER` env var if
   that lookup fails).

The chosen name is sanitized to `[a-zA-Z0-9_-]+` — invalid chars are
replaced with `_` — so it slots safely into a NATS KV key. Empty
after sanitization is a startup error.

## Charm / Lipgloss empirical patterns

These come from chat-TUI buildout. They apply to any Bubble Tea /
Lipgloss work in the repo. Each one is here because we hit the
failure mode and paid debugging time.

### Adaptive colors are the default

Every color must adapt to terminal background. Use
`lipgloss.AdaptiveColor{Light: "...", Dark: "..."}` — never a bare
`lipgloss.Color("237")` for anything operators will see.

Reasoning: `colMuted = "8"` is invisible on light terminals;
`colSelectBg = "237"` renders as near-black on light backgrounds.
Bare ANSI codes are theme-dependent and will look wrong half the
time.

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
single spaces — destroys ASCII art, code blocks, leading indents,
and column alignment. Operators will paste code into prompts and
expect the rendering to survive.

Default chat-content wrap: emit lines that fit verbatim (preserve
all whitespace); fall back to word-wrap only on lines that exceed
the width budget.

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
lines so the content hugs the bottom edge. Filling from the top
reads as "empty surface"; bottom-glued content reads as "fewer
messages so far, more will arrive below."

### Restraint over decoration

Don't add visual elements that duplicate information already
conveyed by something else. `●  alice:` and `alice:` (with `alice`
colored) say the same thing — drop the glyph.

Limit hard-coded visual elements; let role-token styles carry the
distinctions. Every additional glyph or marker is operator visual
cost.
