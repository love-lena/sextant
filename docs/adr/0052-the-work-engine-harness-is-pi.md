---
status: accepted
signed_off_by: lena
date: 2026-06-27
---

# The work engine's harness is pi

[ADR-0043](0043-the-pi-harness-is-a-first-class-bus-client.md) made a headless pi
session a first-class bus client. [ADR-0051](0051-the-run-executor.md) made the
coordinator drive a run by dispatching a `work` step to the dispatcher and awaiting
the worker's `run.event` step-done. This ADR records the decision (TASK-236) that
**pi is the work engine's sole harness**: the managed dispatcher spawns pi workers,
and the work engine advances end-to-end on a real install because the pi worker
deterministically reports each step done.

The deciding fact is the producer. A run advances only when the worker emits a
`run.event` on step completion. The pi-bus extension emits that event on the
worker's `agent_end` — deterministically, not by asking the model to remember to
publish — and reports any artifacts the worker produced (so the brief gate passes).
The earlier claude recipe emitted nothing, so a managed run dispatched to it stalled
at the first work step and timed out to `blocked`. Rather than grow a second
producer for claude, we keep the one producer that works: the claude recipe
(`agent.sh`) and its `NeedsClaude` plumbing are removed, and `pi.sh` is the single
reference recipe behind the dispatcher's `--harness` seam. The seam itself is
unchanged — `--harness` is still a plain `sh -c CMD`, so a future "run workflow X"
recipe slots in the same way; pi is the *default and only shipped* recipe, not a
new mechanism.

A managed dispatcher must be able to launch pi on a Homebrew install, where only
binaries ship. Two things travel **inside the sextant binary**, materialized beside
the component's creds at `components start` (the same discover-then-bake exec
indirection the dispatcher already uses): the `pi.sh` recipe (as before) and the
**pi-bus extension bundle**. The bundle is a single self-contained ESM file —
esbuild bundles the extension with its `@sextant/sdk`, conventions, and `typebox`
dependencies inlined; the pi host (`@earendil-works/pi-coding-agent`) is a
type-only import, so it stays external and is provided by pi at load time. It is a
generated, gitignored embed (like the dash UI bundle): a build that skips it fails
to compile rather than ship a dispatcher that cannot launch a worker. So a brew
install needs no `node_modules` and no node at *build* time on the operator's box —
only `pi` and `node` on PATH at *run* time, which `components start` checks for and
fails loud about. The Anthropic key file is now shared: violet and the dispatcher
both read it (pi runs a real model), so one `sextant secret set anthropic` keys the
managed setup.

This composes with the locked core untouched: the dispatcher is an ordinary bus
client, the recipe is data, the producer is a convention over Messages + Artifacts.
It **amends [ADR-0028](0028-byo-harnesses-join-through-a-plugin-adapter.md)** (BYO
harnesses remain first-class clients via the MCP plugin, but the *work engine's*
dispatched worker is pi, not an arbitrary BYO harness) and **amends
[ADR-0043](0043-the-pi-harness-is-a-first-class-bus-client.md)** (pi is not merely
*a* first-class client but *the* work-engine harness). The convention seam means a
fork that wants a different harness writes a different recipe + a producer that
emits `run.event`; the bright line is the producer, not the model.
