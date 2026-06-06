---
status: accepted
signed_off_by: lena
date: 2026-06-05
---

# The dash is a composable pane cockpit

The dash assembles **pane-surfaces** into a layout the operator controls, and
ships a **cockpit** as the default assembly. This sharpens
[ADR-0014](0014-the-tui-is-a-client.md) ("the dash composes pane-surfaces") into
the concrete mechanism the build follows. The M4 dash mounts the panes whose
data exists now — **presence** (the clients registry), the **message stream**,
and **artifact** (a `document` record) — and the workflow pane waits for real
`sextant.workflow/v1` data (M5).

**Layout is presets + toggling + reflow, not free placement.** The dash ships a
few built-in **preset** layouts; the operator **toggles** which panes show, the
grid **reflows** to fill, and a config file **persists** the choice. This is the
btop model, and it is what "composable" means here — real arrangement control,
bounded. Free placement and live drag are deferred, and the layout config is
shaped so they can arrive later without a rewrite. **Detail-on-demand** is the
same principle at pane scale: the detail pane is hidden and toggled, never an
always-on column.

**widget ⊂ surface ⊂ dash.** Three in-tree strata, each touching only the layer
below (ADR-0014). **Widgets** are generic Bubble Tea pieces (cursor list, stream
viewport, detail pane) that render from theme tokens and import no SDK.
**Surfaces** are the panes, built on widgets against a small contract — set
size, take/lose focus, render their own content, emit intents
(`OpenMsg`/`DoneMsg`) rather than quitting or addressing each other, and declare
an id + title so the layout can toggle them — touching only the public SDK
(ADR-0017). The **dash** composes surfaces through the layout engine and holds
the one identity. A thin `tea.Cmd` adapter re-yields an SDK subscription as
messages; the old `Source`/`Pump` multiplexer stays collapsed into the SDK
(ADR-0014).

**The message surface is one read-stream plus an optional compose.** "Tail"
(observe) and "chat" (participate) are two configs of a single surface, not two
panes and not an addressing model. Its two data flows — the **read side** (the
subscription) and the **send side** (your publishes) — merge by **round-trip**:
the surface subscribes to the topic it publishes to, and a sent message appears
when the bus echoes it back (ADR-0005: durable stream, every consumer sees it).
One render source, no optimistic echo in the contract — *visible* means
*durably on the bus*. Optimistic echo is a later library affordance that does
not change this. Addressing (one shared room, topics, direct) is a subject-level
convention layered on later, not a fork in the surface.

**Forkable, Go-only, no special privilege.** The dash is its own client binary
(`cmd/sextant-dash`) with a thin `sextant dash` alias for convenience — just
another client over the SDK, holding no privileged path (ADR-0014). The library
(theme · widget · surface · layout) is reference-client tooling; another
language can build its own dash, but Sextant invests in one deep Go stack.

Map (ADR-0003): the dash (a human-UI client), the in-tree TUI library it imports
(theme, widgets, surfaces, the layout engine), and the SDK subscription +
`document`/`chat.message`/`client` records it renders. Sharpens ADR-0014;
supersedes nothing.
