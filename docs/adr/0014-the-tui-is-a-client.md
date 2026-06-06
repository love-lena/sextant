---
status: accepted
signed_off_by: lena
date: 2026-06-02
---

# The TUI is a client

A TUI is a **client** — a process that speaks the protocol through the SDK, like
any other. It holds no privileged path to the bus: anything it shows or does,
another client could. The **CLI and the TUI are siblings on the SDK**, not a
stack — "every TUI augments a CLI that works without it; the daemon is the floor"
becomes **the protocol and SDK are the floor, and a plain equivalent always
exists because everyone goes through the SDK.**

**One reference client: the dash.** The dash is a single client — one process,
one identity, many subscriptions (ADR-0008) — composing pane-surfaces. A surface
is a `tea.Model` runnable standalone (the dash showing one surface) or mounted as
a pane with no code change; surfaces draw only their content and emit intents
(`OpenMsg`/`DoneMsg`) rather than quitting or addressing each other. The dash is
**forkable**, not built-in — a stripped chat TUI, or a TUI in another language,
is just another client. Sextant ships the Go reference and no special privilege.

**A component library the dash imports.** Two strata: a generic Bubble Tea
**widget toolkit** (cursor list, stream viewport, detail pane) and the **surface
components** built on it; the dash imports the components, the components import
the widgets. It is reference-client tooling — in-tree, touching only the public
SDK (ADR-0004). The old `Source`/`Pump` that multiplexed RPC + subscribe + file
tail collapses into the SDK: the SDK is the one source, and the library keeps
only a thin `tea.Cmd` adapter that re-yields a subscription as messages.

**The dash is one process, so panes share state in memory.** The old shared-UI
-state subsystem — a `ui_state` KV bucket with operator-scoped selection/focus
keys — existed because separate UI processes could not. It is gone. Cross-client
following (one UI tracking another) is not a special mechanism; it is the
**Artifact** primitive — a single-author owner writes, others watch — reached for
only if it is ever wanted.

**The presentation opinions carry, Go-only.** The base16 theme + role tokens, the
superfile/btop visual language, the locked keybindings, the hard-won lipgloss
patterns, and the test loop (teatest goldens, VHS screenshots, preview binaries,
verify-in-a-PTY) are architecture-independent and carry whole. They are Bubble
Tea / lipgloss opinions, so the reference TUI and library are **Go only** —
another language can build its own TUI, but Sextant invests in one deep Go stack,
not two shallow ones. The status bar keeps its shape; its content is
client-specific, not a fixed daemon counter.

Map (ADR-0003): Clients (the dash, a human-UI client), the SDK (the floor it
speaks), and Artifacts (cross-client UI coordination, if ever).
