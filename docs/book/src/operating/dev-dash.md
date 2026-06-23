# Dev dash: side-by-side testing against the live bus

Run a development `sextant-dash` on a free port alongside the managed production
dash — no swap, no taking prod down, A/B-comparable.

## The one-liner

```sh
sextant-dash --port 0 --ui <worktree>/clients/go/apps/internal/dashapi/web/app
```

`--port 0` asks the OS to assign a free port, so the dev server never collides
with the managed dash (default `:8765`). The binary prints its URL on start:

```
sextant-dash listening on http://127.0.0.1:<N>?token=...
```

## `sextant-dash`, not `sextant dash`

Use the **`sextant-dash` binary** directly. The `sextant dash` sub-command no
longer serves the web dash after the binary split (ADR-0046); it is now a
pointer only. Running the binary directly is always correct.

## When to use `--ui` vs. rebuild the binary

`--ui <dir>` serves the SPA, JSX, CSS, and favicon from the given directory
off disk — with no Go rebuild. Use it for **UI-only changes** (frontend,
styles, assets).

For **Go-side changes** (server logic, the mint path, flag handling), rebuild
the `sextant-dash` binary and restart:

```sh
go install ./clients/go/apps/dash
sextant-dash --port 0
```

## Why two dash servers coexist cleanly

`sextant-dash` holds no standing bus connection (ADR-0046): it connects only to
mint a session credential, then closes. Each browser tab is its own co-equal
client. Two dash servers on different ports therefore do not collide — neither
is a phantom watcher on the bus while idle. The managed prod dash keeps running
as a component; your dev server runs alongside it. Open both tabs and compare
them against the same live bus.

## Quick reference

| Flag | Purpose |
|------|---------|
| `--port 0` | OS-assigned free port (no collision with prod on `:8765`) |
| `--ui <dir>` | Serve SPA from disk; no rebuild needed for frontend changes |
| `--allow-origin <origins>` | Extra browser origins the server accepts (comma-separated) |
| `--context <name>` | Saved context to connect as (default: active context) |
