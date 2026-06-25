---
status: accepted
signed_off_by: lena
date: 2026-06-24
---

# Clients, conventions, and tools are three kinds of module

The repository had grown a language-shaped layout — `clients/go/…` and
`clients/ts/…` — that hid what each directory is *for*. Several directories were
quietly two things at once: `apps/workflow` and `apps/dispatch` each bundled a
record contract with the process that drives it, and a browser client lived
inside a Go app's `internal/` tree. This records the shape that sorts every unit
into exactly one kind, and the layout that follows.

## Three kinds, one test

Every unit is exactly one of:

- a **client** — a process that connects to the bus and holds an identity
  (`sextant.Connect`, subscribe, publish);
- a **convention** — record types and verbs others follow, with no identity of
  its own (a `records.go` / lexicon binding);
- a **tool** — build- or dev-time code that reads the protocol or SDK and emits
  output, and never connects.

The test is "what is this *for*", not where it lives. A unit cannot be two kinds;
where one looks like two, it is two things in one directory and splits — the
contract to the conventions tier, the process to a client. `apps/workflow`
becomes the `workflow` convention plus the `coordinator` client; `apps/dispatch`
becomes the `spawn` convention plus the `dispatcher` client; `docgen` is a tool
and moves under the SDK.

## Clients are flat, vertical peers

Clients are peers — none privileged, grouped by role, not by language. A
`clients/` directory holds each as its own vertical module that imports an SDK: a
CLI, a web-dash server and its browser SPA, a terminal TUI, a coordinator, a
dispatcher, an assistant, an MCP bridge, and harness plugins. A **harness
plugin** (the Claude Code plugin, pi-bus) is the role of a client that makes an
existing agent runtime a first-class bus client — the shape
[ADR-0043](0043-the-pi-harness-is-a-first-class-bus-client.md) and
[ADR-0044](0044-the-browser-dash-is-a-direct-ws-client.md)
already established for pi and the browser, generalised. The language a client is
written in is a detail inside it, not a fork in the path.

## Conventions are a promoted, offered tier — not core

Conventions move above the clients into their own tier: the opinionated, offered
ways to use the primitives (`goal`, `review`, `assistant`, `workflow`, `spawn`).
They stay optional and forkable, and stay out of the locked core. The reference
conventions claim their lexicon namespace at the auth level — "this is what a
goal looks like" — while a fork is free to define its own `mygoal`. Each
convention's definition (lexicon types, verb signatures, conformance vectors) is
anchored in `protocol`; only the hand-written verb logic lives in the tier,
co-located per language so a missing language shows in one listing.

## The SDK stays thin; co-equality is behaviour and shape, not coverage

The SDK exposes universal primitives; Go-host policy built on those primitives —
context profiles, self-enrollment, the durable resume cursor, the build stamp —
stays beside the clients in `shared/go`, never in the SDK. Promoting it would
widen the Go SDK past the TypeScript one. Co-equality binds the two SDKs to the
same wire behaviour and the same interface shape; it does not require the
conventions to match in coverage. A convention present in one language and absent
in another is acceptable drift when it is contained to the components that need
it and declared — the shared definition in `protocol` keeps coverage free to lag
while behaviour can never fork.

## The target tree

```
sextant/
  protocol/                 # contract (core, locked): wire · wireapi · sx · lexicons · conformance
  bus/                      # the server; internal/backend/ holds the backend seam
  sdk/                      # libraries
    go/  ts/                #   the two co-equal SDKs
    docgen/                 #   tool: SDK reference docs (was apps/docgen)
  conventions/              # promoted, offered tier — optional, forkable, NOT core
    goal/       { go/ ts/ }
    review/     { go/ ts/ }
    assistant/  { go/ ts/ }
    workflow/   { go/ ts/ } #   contract split out of apps/workflow
    spawn/      { go/ ts/ } #   contract split out of apps/dispatch
  shared/go/                # cross-cutting Go-host helpers — never imported by the SDK
    clictx/  selfenroll/  seqcursor/  version/
  clients/                  # flat vertical peers — one role each, never nested
    sextant-cli/            #   cli      (command stays `sextant`)
    sextant-dash/           #   service  web-dash server; holds dashapi/ dashserve/
    sextant-tui/            #   tui      terminal cockpit; holds internal/tui/ (widget·theme·surface·layout)
    web-dash/               #   browser  the SPA (was apps/internal/dashapi/web/app)
    coordinator/            #   service  drives conventions/workflow (was apps/workflow)
    dispatcher/             #   service  implements conventions/spawn (was apps/dispatch)
    assistant/              #   service  conventions/assistant; runtime id "violet" (was apps/violet)
    sextant-mcp/            #   service  harness bridge
    pi-bus/                 #   harness  (was clients/ts/pi)
    claude-code/            #   harness  rides sextant-mcp
  tests/  docs/  backlog/  scripts/  tools/  Formula/  .github/
  go.mod  CONTEXT.md  AGENTS.md  …
```

One root `go.mod` still spans the Go packages; the move is within the single
module. The `tui` component library nests inside `sextant-tui` (only it uses it,
now that the dev galleries are retired — TASK-190); `dashapi`/`dashserve` move
into `sextant-dash` (its own
HTTP face); the ex-`clientkit` helpers stand on their own under `shared/go`.

## Consequences

The move is large but mechanical: `sdk/`, `conventions/`, and the Go `shared/`
and `tui` libraries rise to the top; five clients rename or split; a browser
client and a harness plugin relocate out of language trees; every import path and
the `importcheck` rules are rewritten within the one Go module. Because the
intermediate states are painful to inhabit, it lands as one orchestrated change
rather than incrementally — green at the end against the conformance suite, the
rewritten `importcheck` rules, and the full build/test gate.
