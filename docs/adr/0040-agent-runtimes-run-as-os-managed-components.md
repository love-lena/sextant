---
status: accepted
date: 2026-06-18
---

# Agent runtimes run as OS-managed components

The three agent runtimes — `sextant-dispatch`, `sextant-violet`, and
`sextant-workflow` — ship in the Homebrew formula alongside `sextant`,
`sextant-mcp`, and `sextant-dash`, so a normal `brew install sextant` puts them
on PATH. An operator manages them through `sextant components` — a thin CLI
front-end that writes and supervises its own per-component launchd agents; the
OS does the supervising. This ADR records the decisions behind that shape.

## Why the runtimes are installed via the formula

v0.5.1 shipped violet's surface — the mobilise command, the FAB panel, the
dispatch integration — but the dispatcher binary was never installed or running.
The button existed and nothing was behind it. v0.5.3 closes that gap: the
binaries arrive with the formula, so install means operational, not
build-artifact-present-somewhere.

The bus stays the single Homebrew-managed service (`brew services start
sextant`). One service per formula is the brew convention, and the runtimes are
components on top of a running bus — they cannot sensibly run as parallel
first-class brew services. The bus is the foundation; `sextant components`
manages the layer above it.

## Components own their launchd agents

`sextant components` writes a launchd plist at
`~/Library/LaunchAgents/dev.sextant.<name>.plist` for each component, then
bootstraps and supervises it through `launchctl`. The operator-facing surface is
one ergonomic command: `sextant components status|start|stop|restart [name|--all]`.
No pid-hunting, no scattered log scraping — `sextant doctor` rolls up runtime
health alongside the bus.

This is the right split between what the OS manages and what the client does.
The OS supervises processes; `sextant components` is the thin operator front-end
for the launchd layer. The bus core never supervises client processes — that was
the deleted control-plane reconciler, and its deletion is the signal-not-manage
discipline: the bus signals and cooperates with its clients; it does not track or
manage them ([ADR-0033](0033-a-dispatcher-mints-its-own-workers.md) is a
dispatcher's own workers, not the bus's client processes).

`start` runs bootstrap → kickstart → health-check rather than bootstrap alone.
A bootstrap can leave a job loaded but with `runs=0` — launchd accepted the plist
without actually starting the process. The health-check makes that visible and
loud immediately, consistent with the fail-loud discipline (TASK-118): a
component whose recipe is unsatisfied — `claude` absent from PATH for the
dispatcher, no API key for violet — refuses to start rather than loading a plist
that will silently fail on every keepalive attempt.

## The `components exec` indirection

Each component's plist runs `sextant components exec <name>` rather than the
runtime binary directly. `exec` resolves the environment in Go — `PATH` extended
to include the directory of the sextant binary (so `claude` and other
co-installed tools are findable) and `SEXTANT_MCP_BIN` set — and for
key-bearing components loads the API key from a `0600` env-file at exec time via
`syscall.Exec` into the real runtime. The key is **never written into a launchd
plist**, and it is never held in memory longer than the exec boundary.

This is one testable seam that solves two distinct problems at once: the
minimal-PATH that launchd provides (it does not inherit the user's login shell
PATH), and the secure secret-loading requirement. A single Go function can be
unit-tested; a hand-crafted plist embedding secrets cannot.

## Component identity

Each component mints its own scoped bus identity on first start, using the
canonical held-mode `sextant clients register` flow
([ADR-0020](0020-clients-are-bus-issued-identities.md)). It runs with
`--creds <its own file>` explicitly — never the operator's active context.
Components are top-level registered clients with their own bus-issued ULIDs and
their own `SpawnedBy`-free records, consistent with
[ADR-0033](0033-a-dispatcher-mints-its-own-workers.md)'s
top-level-client-may-dispatch rule. There is no principal claim and no
activation — components adopt the designated principal as peers do
([ADR-0029](0029-a-harness-speaks-as-itself.md)), they do not become it.
`sextant secret set anthropic` stores violet's API key in the `0600` env-file
that `exec` loads.

## Harnesses reach the bus through the plugin adapter ([ADR-0028](0028-byo-harnesses-join-through-a-plugin-adapter.md)); the dispatcher and violet are both clients over the SDK

No new integration path is introduced. The runtimes are clients like any other —
they connect via `sextant-mcp` or directly via the SDK — distinguished by their
role, not by any privileged tier the bus learns about. The OS-management layer is
entirely outside the protocol.

## Consequences

- The operator manages runtimes via `sextant components`; `sextant doctor`
  surfaces their health alongside the bus, so "is everything running?" has one
  command.
- This milestone is macOS-only (launchctl). A Linux caller receives a clear
  "not yet" message rather than a partial or silent failure; systemd support is a
  tracked follow-up.
- Deferred fast-follows: running the dash as a managed component (it currently
  runs attached to a terminal) and a `LoadKeyEnv` read-permission check to warn
  early when the env-file's mode would prevent the key from loading.
- The run-management model composes with the leaf-node topology
  ([ADR-0038](0038-a-remote-box-joins-through-a-leaf-node.md)): a remote box
  that joins as a leaf runs its own `sextant components` for its local runtimes;
  those clients reach the hub through the leaf link, indistinguishable from any
  other client so far as the protocol is concerned.
