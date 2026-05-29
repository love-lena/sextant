# RFC: The sextant TUI workstream — shared widgets + the surface catalog

**Status:** Draft for review
**Author:** Claude (with Lena)
**Date:** 2026-05-28
**Scope:** Everything shippable on the *existing* backend. No new RPC verbs, no schema changes, no daemon work.

## TL;DR

sextant has two interactive surfaces today (`agents`, `chat`). Both are
**hand-rolled** — they share a *mount* contract (`Component` / `Host` /
registry) but **zero widgets**. Every list is a bespoke `fmt.Sprintf`
table; every scroll region is bespoke. The next ~10 interactive
surfaces we want would each re-hand-roll the same list / scroll / detail
rendering, with subtly different keybinds and empty-states.

This RFC proposes building the **widget layer** the 0.3.0 wave skipped:

1. **Three shared widgets, built once** — `ListPane[T]`,
   `StreamViewport`, `DetailPane` — plus a `stream.Source` adapter that
   unifies "NATS subscribe / file tail / one-shot RPC."
2. **Retrofit `agents` and `chat`** onto them, so the two existing
   surfaces and every new one share one vocabulary.
3. **Deliver the surface catalog as thin compositions.** Each new TUI
   becomes ~a screen of glue, not a from-scratch model.

**The whole P0–P2 catalog ships with zero backend work.** Every surface
is a new *front-end* over an RPC verb, NATS subject, or file path that
already exists — verified against the RPC catalog (§6). (One *optional*
P3 surface, `templates list`, may want a trivial read endpoint; flagged,
not hidden.)

Delivered in four independently-shippable phases:

| Phase | Contents | Release |
|-------|----------|---------|
| **P0** | The three widgets + `stream.Source`; retrofit `agents` (+`chat`) | (internal — no operator-visible change) |
| **P1** | `pending`, `traces`, `context` (-i) — the already-planned three, now thin | v0.4.0 |
| **P2** | `worktree list`, `daemon logs`, `audit list`+`tail`, `agents show` detail | v0.5.0 |
| **P3** | events monitor, files browser, history, templates, theme, doctor | as-wanted |

---

## 1. Motivation

`PRINCIPLES.md` makes two claims this workstream leans on directly:

> **User ergonomics is a first-class deliverable.** … Treat ergonomic
> gaps the same way as correctness bugs.

> **The cost of late visual feedback compounds.** Each subsequent layer
> … gets built on the wrong … foundation and has to be partially redone.

Hand-rolling compounds the same way. Today `pkg/tui/agents/model.go`
hand-rolls a cursor table (`renderTable`, manual `j/k/g/G`); `pkg/tui/chat`
hand-rolls frame wrapping and scrollback in its own `view.go`/`turn.go`.
Neither uses `bubbles/list`, `bubbles/table`, or `bubbles/viewport` —
those packages are **not imported anywhere in the repo**. The result:

- **Inconsistency is already leaking.** Two surfaces, two scroll models,
  two empty-state conventions. With ten surfaces this becomes a tax the
  operator pays on every screen ("does `g` go to top here, or is it `Home`?").
- **Every new surface pays full freight.** The Thread-A plan
  (`plans/feat-0.4.0-interactive-surfaces-impl.md`) would hand-roll
  *three more* tables/viewports. The P2/P3 candidates would hand-roll
  ~seven more.

The 0.3.0 release shipped the **composition** layer — `sextant dash`
(multi-pane), `sextant tui` (discovery menu), the `-i` flag + component
registry. That's the frame. **We have a frame and no bricks.** This RFC
makes the bricks.

The leverage case: three widgets, each consumed by **≥3 surfaces**, turn
the catalog from "N bespoke builds" into "N thin assemblies." The right
moment to extract is *before* the next seven surfaces, not after.

---

## 2. The four archetypes

Survey every `sextant` command and each interactive surface falls into
one of four shapes:

| Archetype | Operator question | Widget | Surfaces |
|-----------|-------------------|--------|----------|
| **List** | "what are my Xs, and let me act on one" | `ListPane[T]` | agents, pending, worktree, audit, files-ls, templates, theme |
| **Stream** | "show me this content / tail it" | `StreamViewport` | context, daemon logs, files-read, audit-tail, events, worktree-diff |
| **Detail** | "everything about this one X" | `DetailPane` | agent detail, trace-span detail, template detail, doctor |
| **Tree** | "this hierarchy, collapsibly" | `ListPane[T]` (flattened rows + depth) | traces, subagent tree (context mode 6) |

**Tree is not a fourth widget** — it's a `ListPane` fed a pre-flattened,
depth-annotated row slice (the `BuildSpanTree` / `FlattenVisible`
projection). So **three widgets cover four archetypes.**

---

## 3. Proposed widgets

All three live in a new **`pkg/tui/widget/`** package — deliberately
distinct from `pkg/tui/component/` (which owns the *mount* contract:
`Component`, `Host`, registry, intent messages) and from
`pkg/tui/<surface>/` (the surfaces themselves). A widget is a reusable
sub-model a surface embeds; it is **not** a `Component` (it has no
lifecycle/registry identity of its own). See Open Question 2.

Every widget consumes `pkg/theme` role tokens — no bare palette indices,
matching `conventions/tui-conventions.md`.

### 3.1 `ListPane[T]` — the workhorse

A generic, theme-aware, cursor-driven list. Replaces the hand-rolled
`renderTable` in `agents` and the three tables P1 would otherwise build.

```go
package widget

// ListPane is a generic cursor list. T is the row payload; the surface
// supplies a RowRenderer that turns a T into a styled line. ListPane
// owns navigation, selection, the header, the empty-state, and (opt-in)
// a filter line and per-row actions. It is NOT a Component — a surface
// embeds it and forwards Update/View.
type ListPane[T any] struct { /* rows, cursor, header, filter, theme, size */ }

type ListConfig[T any] struct {
	Header    string                       // column header line (already laid out)
	Render    func(row T, selected bool) string
	Empty     string                       // empty-state text
	Filter    func(row T, query string) bool // nil = no `/` filter
	KeyID     func(row T) string           // stable id for selection/OpenMsg
}

func NewList[T any](cfg ListConfig[T]) ListPane[T]

func (l *ListPane[T]) SetRows(rows []T)        // re-render; clamp cursor
func (l *ListPane[T]) SetSize(w, h int)
func (l *ListPane[T]) Update(msg tea.Msg) (ListPane[T], ListAction[T]) // j/k/g/G, `/`, enter
func (l *ListPane[T]) View() string
func (l *ListPane[T]) Selected() (T, bool)     // current row
```

`Update` returns a `ListAction[T]` (a small sum: `None`, `Selected{row}`,
`Filtered`) so the **surface** decides what selection means (emit
`component.OpenMsg`, run a row action, etc.) — the widget never addresses
other panes itself, per the routing convention.

**Opt-in extras, gated by YAGNI** (built only when a consumer needs them):
filter line (`/`), and a row-action keymap (`m`→merge, `d`→delete) for
`worktree list` in P2.

**Build choice — hand-rolled generic, *not* `bubbles/table`/`bubbles/list`.**
`bubbles/table` fixes column styling and won't take our per-row role
tokens cleanly; `bubbles/list` brings a delegate model + built-in
filtering/pagination we mostly don't want and would have to fight. We
already hand-roll the exact ~30 lines in `agents` — generalizing that
into `ListPane[T]` is less code than adapting either bubble, and keeps
theme-token control. (Open Question 1.)

### 3.2 `StreamViewport` — scroll + tail

Wraps `bubbles/viewport` (we **adopt** viewport here — there is no reason
to hand-roll scrolling). Adds the three things every tailing surface needs
and currently reinvents:

```go
package widget

type StreamViewport struct { /* vp viewport.Model, lines ring, follow bool, theme */ }

func NewStreamViewport(maxLines int) StreamViewport // ring-buffer cap

func (s *StreamViewport) SetSize(w, h int)
func (s *StreamViewport) SetContent(lines []string) // replace (one-shot dump)
func (s *StreamViewport) Append(lines ...string)    // tail; sticks to bottom unless scrolled up
func (s *StreamViewport) Update(msg tea.Msg) (StreamViewport, tea.Cmd) // j/k, PgUp/Dn, g/G, ^d/^u
func (s *StreamViewport) View() string
func (s *StreamViewport) Following() bool            // for the chrome's follow indicator
```

- **Tail/autoscroll:** stays pinned to the bottom while new lines arrive,
  *unless* the operator has scrolled up (then it holds position and shows
  a "▼ following off" hint until they `G` back to bottom).
- **Line budget:** a ring buffer (`maxLines`) so an overnight `daemon logs`
  or `events tail` can't OOM the process.
- **Consistent scroll keys** across every stream surface.

`search` (`/` to find within the buffer) is a P3 addition, not P0.

### 3.3 `DetailPane` — the inspector

Label/value rows grouped into titled sections. Trivial to hand-roll;
worth standardizing so `agent detail`, trace-span detail, template
detail, and `doctor` look identical.

```go
package widget

type DetailPane struct { /* sections, theme, size, scroll via embedded StreamViewport */ }

type Section struct {
	Title string
	Rows  []Row // {Label, Value} — Value already styled (e.g. a lifecycle dot)
}

func NewDetail() DetailPane
func (d *DetailPane) SetSections(secs []Section)
func (d *DetailPane) SetSize(w, h int)
func (d *DetailPane) Update(msg tea.Msg) (DetailPane, tea.Cmd) // scroll only
func (d *DetailPane) View() string
```

Long detail (e.g. an agent with many recent frames) scrolls via an
embedded `StreamViewport` — composition, not duplication.

### 3.4 `stream.Source` — the data adapter

Today the goroutine that feeds a surface is copy-pasted per surface:
`agents` has three (`drainLifecycle`/`drainPending`/`drainSelectedAgent`),
`pending` and `context` each get their own. They all do the same shape:
*open a source → for each item, push a `tea.Msg` via the package
`SetSender`*. Unify it:

```go
package widget // or pkg/tui/widget/stream

// Source is anything that yields items until closed: a NATS subscription,
// a tailed file, or a one-shot RPC that emits once and closes.
type Source[T any] interface {
	Events() <-chan T
	Close() error
}

func SubscribeSource(cli Bus, subject string, opts ...client.SubscribeOption) Source[client.Message]
func TailSource(path string) Source[string]   // wraps nxadm/tail
func OnceSource[T any](fn func() (T, error)) Source[T]

// Pump drains src into send until the channel closes or ctx cancels.
func Pump[T any](ctx context.Context, src Source[T], send func(tea.Msg), wrap func(T) tea.Msg)
```

This collapses the per-surface drain boilerplate to one line each and is
the single seam tests fake.

### 3.5 Theming + size discipline

Widgets take their styles from `pkg/theme.Theme` at construction (mirror
`pkg/tui/agents/theme.go`'s `themeFor`). Hosts push size via the existing
`Component.SetSize` → surface → `widget.SetSize` chain. No widget reads
the terminal directly.

---

## 4. Retrofit: `agents` + `chat`

Per the chosen strategy (primitives first **+ retrofit**), the two
existing surfaces migrate onto the widgets so there's exactly one list
implementation and one scroll implementation in the tree.

- **`agents`** — `renderTable` + cursor state → `ListPane[AgentSummary]`.
  The bus wiring, lifecycle/pending drains, and `selected_agent` KV
  behavior are untouched; only the rendering + nav move into the widget.
- **`chat`** — the scrollback region → `StreamViewport`, fed by the
  existing turn renderer (turns become the line producer). The chat's
  input textarea, lifecycle header, and frame model stay.

**This touches shipped, working code — the biggest risk in the RFC.**
Mitigation:

1. **Lock current behavior first.** Before refactoring, ensure the
   golden/VHS snapshots + reducer tests pin today's output. The retrofit
   is then **green-to-green**: identical rendered frames, identical
   keybinds.
2. **`chat` is the harder retrofit** (its wrapping logic is entangled
   with turn rendering). If it fights `StreamViewport`, **defer `chat`'s
   retrofit to P3** and ship `agents` + the new surfaces on the widgets
   first. `agents` alone proves `ListPane`. (Open Question 3.)

---

## 5. Surface catalog

Every interactive surface, its backing data path, and its status. **The
"backend work" column is `none` for every row** — that is the thesis.

| Surface (`-i` / TUI) | Archetype | Widget(s) | Backend source (exists today) | Status | Backend work |
|----------------------|-----------|-----------|-------------------------------|--------|--------------|
| `agents list` | list | ListPane | `list_agents` RPC | shipped → retrofit | none |
| `agents show <id>` | detail | DetailPane | `get_agent_status` + sessionlog + `query_history` | **new (P2)** | none |
| `agents chat` / `conversation` | stream | StreamViewport | `agents.<uuid>.frames` subject | shipped → retrofit | none |
| `pending list` | list | ListPane | `user_input.>` subject | planned (P1) | none |
| `traces show <id>` | tree | ListPane (tree) | `query_trace` RPC | planned (P1) | none |
| `agents context <id>` | stream | StreamViewport + TailSource | `get_agent_status.SessionLog` + bind-mounted JSONL | planned (P1) | none |
| `worktree list` | list + actions | ListPane | `worktree_list` RPC (+ `merge`/`destroy`/`diff`) | **new (P2)** | none |
| `daemon logs` | stream | StreamViewport + TailSource | daemon log **file** (`resolveLogPath`) | **new (P2)** | none |
| `audit list` | list | ListPane | `query_audit` RPC | **new (P2)** | none |
| `audit tail` | stream | StreamViewport + SubscribeSource | audit NATS subject | **new (P2)** | none |
| `events tail` | stream | StreamViewport + SubscribeSource | events subject | future (P3) | none |
| `files ls` + `files read` | list↔stream (master/detail) | ListPane + StreamViewport | `list_dir` + `read_file` RPCs | future (P3) | none |
| `history` (per agent) | stream/list | StreamViewport or ListPane | `query_history` RPC (client method exists; no CLI sibling) | future (P3) | none |
| `templates list` | list | ListPane | templates KV (no list RPC today — `templates` has only `reload`) | future (P3) | **minor*** |
| `theme list` | list (preview) | ListPane + DetailPane | `pkg/theme` (local) | future (P3) | none |
| `doctor` | detail/report | DetailPane | `get_version` + reachability checks | future (P3) | none |

\* `templates list` is the **one** surface in the catalog that may want
a small new read endpoint (`list_templates`) — there's a `templates
reload` path but no list RPC. It's P3/optional and the endpoint is
trivial; flagged here for honesty rather than buried. Everything in
P0–P2 is confirmed front-end-only.

---

## 6. Backend readiness (the verified claim)

Mapped against `pkg/rpc` + the NATS subjects the CLI already subscribes:

```
list_agents      ✓  get_agent_status ✓  query_trace   ✓  query_audit   ✓
query_history    ✓  worktree_list    ✓  worktree_diff ✓  worktree_merge ✓
worktree_destroy ✓  list_dir         ✓  read_file     ✓  get_version    ✓
user_input.> (subject) ✓   audit/events (subjects) ✓   daemon log file ✓
agents.<uuid>.frames (subject) ✓   session JSONL bind-mount ✓ (0.3.0)
```

Each **P1/P2** surface's CLI sibling already issues exactly that
RPC/subscribe/read — the TUI is a different renderer over the **same**
call. The P3 surfaces read existing RPCs/subjects/KV even where no CLI
sibling exists yet (`history` has a `query_history` handler **and** a
`pkg/client` method, just no dedicated command). So **no P0–P2 surface
requires a new verb, payload field, or daemon change.** The lone
possible exception is `templates list` (P3), which may want a trivial
`list_templates` read endpoint — flagged in the catalog, not hidden.

**The one nuance — `agents show` detail.** `get_agent_status` returns
`{UUID, Name, Lifecycle, Version, UpdatedAt, Heartbeat?, SessionLog?}`.
The richer inspector (template, worktree, usage, recent frames) is
**assembled client-side**: template/worktree from `list_agents` +
`worktree_list`, usage from the already-mounted session JSONL
(`pkg/sessionlog` usage mode), recent frames from `query_history` (durable)
or the live `frames` subject. A future `get_agent_detail` RPC could
consolidate these into one call — but it is an **optimization, not a
prerequisite**; P2 ships on the existing calls. (Open Question 4 picks
the frames source.)

---

## 7. Phasing

Each phase ships independently, cuts its own release, and leaves `main`
working. Per `conventions/versioning.md`, the bump is read off the
changelog at cut time (all entries here are additive → MINOR).

### P0 — Widgets + retrofit *(no operator-visible change)*
Build `ListPane`, `StreamViewport`, `DetailPane`, `stream.Source` in
`pkg/tui/widget/`, each TDD'd in isolation. Retrofit `agents` (and `chat`,
unless deferred per OQ3). **Success = the retrofit is green-to-green** —
the existing golden/VHS snapshots are unchanged. This phase de-risks
everything downstream; it intentionally ships nothing new to the operator.

### P1 — `pending`, `traces`, `context` *(v0.4.0)*
The originally-planned three, now thin compositions:
- `pending` = `ListPane[Request]` + `SubscribeSource("user_input.>")`.
- `traces` = `ListPane` over `FlattenVisible(BuildSpanTree(...))` + `query_trace`.
- `context` (-i) = `StreamViewport` + `TailSource(jsonl)` + the
  `pkg/sessionlog` mode renderers (shared with the CLI dump per the
  existing plan's §Task 3.1 extraction).

`plans/feat-0.4.0-interactive-surfaces-impl.md` is **re-cut against the
widgets** for this phase (the bespoke model code in that plan is replaced
by widget composition; the wiring/launcher/registry tasks carry over).

### P2 — `worktree`, `daemon logs`, `audit`, `agent detail` *(v0.5.0)*
- `worktree list` — `ListPane` with the row-action keymap (`m` merge / `d`
  delete / `enter` diff → opens a `StreamViewport` on `worktree_diff`).
- `daemon logs` — `StreamViewport` + `TailSource(logPath)`. Pure file tail.
- `audit list` (`ListPane` over `query_audit`) + `audit tail`
  (`StreamViewport` + `SubscribeSource`). Exercises both widgets on one
  resource.
- `agents show <id>` detail — `DetailPane` (§6). Validates the third widget.

### P3 — the tail *(as-wanted)*
events monitor, files browser (master/detail), history viewer, templates
list, theme picker, `doctor` report. Each is near-free once the widgets
exist; pick them up opportunistically.

**Automatic wins:** because surfaces self-register via `init()`, every new
component appears in the `sextant tui` menu and is mountable as a `sextant
dash` pane with no extra wiring.

---

## 8. Cross-cutting concerns

- **Composition model (unchanged):** widgets → embedded in Components →
  mounted as dash panes / `-i` surfaces. This RFC fills the layer *beneath*
  the 0.3.0 composition layer; it does not touch `dash`/`tui`/registry.
- **Keybinding vocabulary:** one set across all surfaces — `ListPane` owns
  nav (`j/k/g/G`, `/`), `StreamViewport` owns scroll (`PgUp/Dn`, `^d/^u`,
  `g/G`), surfaces add domain keys (`m`/`d` etc.). Consistency becomes a
  property of the widgets, not a thing each surface remembers to honor.
  The widgets expose `ShortHelp`/`FullHelp` fragments the surface merges
  into its `Component` help.
- **Testing:** (a) reducer unit tests per widget (table-driven nav/append/
  filter); (b) golden snapshots for each `View()`; (c) VHS tapes per
  surface via the `feat-tui-vhs-*` infra; (d) the registry double-register
  test. Per `PRINCIPLES.md §3`, ship a runnable mockup / screenshot for
  Lena at each surface's first viable render — *before* wiring network
  plumbing.
- **Backwards compat:** the static (non-`-i`) CLI output for every command
  is untouched. `-i` remains opt-in; `--json` remains the scripting path.

---

## 9. Risks & non-goals

**Risks**
- *Retrofit blast radius.* Mitigated by green-to-green discipline + the
  `chat`-deferral escape hatch (§4, OQ3).
- *Over-abstraction.* Guard: exactly **three** widgets, each with **≥3
  named consumers** in the catalog; opt-in extras (filter, row-actions,
  search) are built only when a consumer lands. No speculative widgets.
- *Generics ergonomics.* `ListPane[T]` is a clean fit for Go generics
  (one type param, one render func); no deep type gymnastics.
- *`bubbles/table` regret.* Considered and rejected (§3.1) — re-open only
  if the hand-rolled generic grows past ~150 lines.

**Non-goals (explicitly out of scope)**
- **Any new backend.** No new RPC verb, payload field, subject, or daemon
  change. (The `get_agent_detail` consolidation RPC is a *future*
  optimization, not part of this workstream.)
- **The chat data-model rework** — history source-of-truth, queued-drain
  semantics — is a separate, decision-gated track
  (`feat-chat-tui-history`, `bug-sidecar-queued-prompt-drain-orphans-context`).
  This RFC only retrofits chat's *rendering*, not its data model.
- **Write-heavy forms** beyond `pending`'s "emit an answer intent." A full
  answer/escalate form is a later, separate design.
- **Mouse** beyond what `sextant dash`'s BubbleZone already provides.
- **Durable cross-restart replay** of any stream.

---

## 10. Open questions

1. **`ListPane` implementation** — thin hand-rolled generic
   (*recommended*, §3.1) vs wrap `bubbles/table`?
2. **Widget package location** — new `pkg/tui/widget/` (*recommended*) vs
   fold into `pkg/tui/component/`?
3. **`chat` retrofit timing** — in P0 for full consistency (*recommended*),
   or deferred to P3 if its bespoke wrapping fights `StreamViewport`?
4. **`agent detail` "recent frames" source** — `query_history` (durable
   ClickHouse snapshot, *recommended* for a stable view) vs live
   `agents.<uuid>.frames` subscribe (ephemeral, but live)? Could offer both
   (snapshot + optional live tail).

---

## Appendix A — archetype × surface matrix

```
                 ListPane   StreamViewport   DetailPane
agents list         ●
agents show                                     ●
chat                            ●
pending             ●
traces              ● (tree)
context                         ●
worktree list       ●
worktree diff                   ●
daemon logs                     ●
audit list          ●
audit tail                      ●
events tail                     ●
files ls            ●
files read                      ●
history                         ●            (or ●)
templates list      ●
theme list          ●                           ●
doctor                                          ●
```

Consumer counts: **ListPane ≥7**, **StreamViewport ≥7**, **DetailPane ≥4**.
Each widget clears the "≥3 consumers" bar comfortably.

## Appendix B — references

- `conventions/tui-conventions.md` — Tier 0/1/2 model, Component contract,
  cross-component routing, keybinding + testing conventions.
- `PRINCIPLES.md` §2 (ergonomics first-class) + §3 (visual feedback early).
- Resolved 0.3.0 tickets this builds on: `feat-cli-iflag-tier1-components`,
  `feat-sextant-dash-multipane`, `feat-sextant-tui-discovery`,
  `feat-tui-component-interface`, `feat-tui-theme-package`.
- `plans/feat-0.4.0-interactive-surfaces-impl.md` — the existing Thread-A
  plan; becomes **P1**, re-cut to compose the widgets rather than
  hand-roll. (`feat-tui-pending-component`, `feat-tui-traces-component`,
  `feat-agents-context-view` Phase B are its tickets.)
- Backend contract: `pkg/rpc` verb constants; `pkg/sextantproto` payloads;
  `pkg/sessionlog` (typed JSONL parser, shipped 0.3.0).
