---
status: accepted
signed_off_by: lena
date: 2026-06-23
---

# The web dash and the terminal UI are two binaries

The dash splits into two distinct, independently-shipped programs. **`sextant-dash`**
is the web dash: a standalone binary that serves the SPA and mints the browser's bus
credential — the role [ADR-0044](0044-the-browser-dash-is-a-direct-ws-client.md)
reduced the dash process to. **`sextant-tui`** is the terminal UI: the former cockpit,
reframed from *the dashboard* to a first-class CLI/TUI feature, with its `--serve`
capability removed entirely. The browser dash is THE dash; the terminal UI is a peer
feature, not a lesser dashboard. This is realised across TASK-186 and its follow-ons
(187–191).

## What it refines (ADR-0044)

ADR-0044 reduced the Go dash process to two jobs — serve the SPA, mint the credential —
but left it holding a bus connection for its whole lifetime. This ADR pins that
connection's *lifetime*: `sextant-dash` connects only to mint a session credential, then
closes. At rest it holds no bus client, so the only connected dash client the bus ever
sees is an open browser tab. **Server-up no longer means client-connected** — which is
what makes a keep-alive dash an honest participant rather than a phantom watcher.

## What it extends (ADR-0040)

[ADR-0040](0040-agent-runtimes-run-as-os-managed-components.md) made the agent runtimes
OS-managed components but deliberately excluded the dash as "the operator's foreground
surface, not a keep-alive runtime." That exclusion rested on the dash behaving like a
standing client. A connect-to-mint-then-close `sextant-dash` no longer does, so it joins
the component Registry: it comes up with the bus, the operator never types `--serve`, and
`sextant components start/stop/restart dash` is its lifecycle.

## Consequences

- `sextant dash` (the verb) and the dash URL now mean the web dash; the terminal UI is
  reached via `sextant-tui`.
- `--serve`, `runServe`, and the dashapi HTTP serving live only in `sextant-dash`;
  `sextant-tui` carries no serve/HTTP code.
- Dev iteration is **side-by-side, not a swap**: a dev `sextant-dash` on a separate port
  (`--port 0 --ui <dir>`) runs against the live bus alongside the managed one, because two
  stateless co-equal dash sessions don't collide. No taking prod down, no restore.
- Two binaries now ship via Homebrew.
- Removing the standing connection also clears the `sx.hb` permissions violation
  (TASK-185).
