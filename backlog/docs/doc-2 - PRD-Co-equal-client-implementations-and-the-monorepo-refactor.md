---
id: doc-2
title: PRD - Co-equal client implementations and the monorepo refactor
type: specification
created_date: '2026-06-19 20:58'
---

Records the plan behind [ADR-0041](../../docs/adr/0041-clients-are-co-equal-across-languages.md). Tickets are the source of truth; this doc is the narrative that frames them.

## Problem Statement

Sextant's value is a protocol for agents to collaborate over a bus, but in practice the system has drifted toward a Go monolith. The only real client implementation is Go; the "modules" are a folder-and-worktree convention rather than genuine deep, decoupled units; the file tree is organised by Go visibility (`pkg/` / `internal/` / `cmd/`) rather than by what the system *is*; and conventions like "set a goal" are hand-written in several places that have already silently diverged (the goals read half and write half drifted on field names — violet renders criteria off `label`/`state` while the lexicon only defines `text`/`status`). The operator cannot read the architecture from the tree, cannot trust that "done" means usable, and has no second-language client proving the protocol is genuinely language-neutral. Before Go assumptions calcify further, the protocol needs to *be* the product, with co-equal client implementations, in a repository whose structure communicates its architecture.

## Solution

Refactor the monorepo around one decision (ADR-0041): the protocol — the lexicon (record shapes, operations, and convention verb signatures) plus a conformance suite — is the language-neutral product. The bus is one Go server, implemented once. The client surface (SDK, conventions, clients) is co-equal across languages, verified by the conformance suite, which *defines* when a client is co-equal. Reorganise the tree domain-first (`protocol/`, `bus/`, `clients/<language>/`) so it reads as the architecture, with `internal/` nested locally for visibility and a single Go module. Make conventions lexicon-defined libraries — generate record types per language, hand-write the verb logic, verify behaviour with recorded conformance vectors. Prove and *force* the model now by building the first non-Go client: a TypeScript SDK driving a **pi** harness extension that makes a pi agent a first-class citizen of the bus. Fold in the deep-module cleanups the new structure enables — collapsing the duplicated convention halves and fixing the goals field-drift bug.

## User Stories

1. As a contributor opening the repo, I want the top-level directories to name what the system is (`protocol`, `bus`, `clients`), so that I can understand the architecture from the file tree without first learning Go's visibility conventions.
2. As a contributor, I want each module to present a small interface over substantial hidden implementation, so that I can use and test it without reading its internals.
3. As the operator, I want the protocol — record shapes, operations, and convention verb signatures — defined once in the lexicon, so that there is a single source of truth for what a correct client does.
4. As a client developer in any language, I want the record types generated from the lexicon, so that my client's types cannot silently drift from the contract.
5. As a client developer, I want a conformance suite of recorded operation transcripts, so that I can prove my implementation behaves identically to the reference before calling it done.
6. As the operator, I want the Go bus to remain the single server implemented once, so that I never pay to reimplement the server in another language.
7. As the operator, I want the Go client SDK to sit as a peer of other-language SDKs rather than privileged at the repo root, so that no single language quietly becomes the whole system.
8. As a TypeScript developer, I want a TS sextant SDK that connects, publishes, reads, subscribes, and works with artifacts, so that I can build a client in TypeScript.
9. As a pi user, I want a pi extension that makes my pi session a first-class citizen of the bus, so that my pi agent participates like any crew member.
10. As a pi agent, I want to be addressed over the bus and woken on inbound messages, so that I respond as a live participant rather than by polling.
11. As a pi user, I want bus tools (publish / read / subscribe / artifacts) and a `/set-goal` command in my pi session, so that I can act on the bus from within pi.
12. As the operator, I want a headless pi workflow session to be addressable over the bus, so that I can interact with it as a client without attaching to its terminal.
13. As the operator, I want to cleanly hand off a headless pi session — drain and stop it, resume it by hand, then let the workflow resume it — so that I can jump into a session without two processes fighting over it.
14. As the operator, I want conventions like "set a goal" implemented as a library over the SDK rather than a bus feature, so that the bus stays primitive and content-opaque.
15. As the operator, I want the goals convention to read and write the same lexicon fields everywhere, so that the dash and violet stop disagreeing about a goal (the `label`/`state` drift bug is fixed).
16. As a maintainer, I want the duplicated goals halves (read and write) collapsed into one module, so that a change to the goal rule touches one place.
17. As a maintainer, I want the durable per-subject cursor implemented once, so that the resume-watermark rule cannot drift across the clients that use it.
18. As a maintainer, I want the SDK to stop returning an internal wire type across its public boundary, so that callers do not couple to internals.
19. As a contributor, I want import direction enforced mechanically — a convention library may import the SDK but never the bus — so that "anything a client does, a bare client could do over the operations" is a CI guarantee.
20. As the operator, I want the conformance suite to define when a client is co-equal, so that a new client is not "done" until it passes the vectors.
21. As a headless or pi worker, I want to hold my own scoped credentials, so that I authenticate as myself and never impersonate the operator.
22. As the operator, I want a spike that validates pi's headless wake, connection survival across session transitions, back-pressure, and the security/trust posture before committing the design, so that I am not building on unproven assumptions.
23. As a future browser client, I want the bus to optionally expose a WebSocket listener, so that a browser can speak the protocol — deferred until a real browser client exists.
24. As the operator, I want the architecture recorded in an ADR and the shared language in CONTEXT.md, so that the decision is canon, not tribal knowledge.
25. As a contributor, I want the layout migration to be a mechanical, behaviour-preserving move, so that I can review it as a rename, not a rewrite.
26. As the operator, I want the deep-module content changes to land on the new tree *after* the move, so that restructure and deepen are reviewed separately.
27. As a client SDK author, I want SDK versions anchored on the protocol epoch, so that compatibility between languages is a clear contract.
28. As the operator, I want each convention (goals, review, home, workflow) owned by one deep module per language, so that the convention's mechanics live in one readable place.
29. As a pi agent developer, I want a bundled sextant skill in the pi package, so that the agent knows the bus conventions without me re-explaining them.
30. As a contributor, I want reference-app internals hidden in a nested `internal/` under each app, so that the public surface is exactly the importable top-level directories.
31. As the operator, I want the dispatcher to spawn and re-spawn pi sessions as scoped bus clients, so that a workflow can run pi agents headlessly and I can rejoin them over the bus.
32. As a maintainer, I want the bus and each client to agree on the frame via the frame lexicon plus conformance vectors rather than a shared codec, so that no client gets a Go-only shortcut.
33. As the operator, I want the operation set (`methods.json`) expressed as operation-lexicons referencing the record lexicons, so that operations and types live in one schema system.
34. As a contributor, I want the revived Go house-style skill and a static-checks gate to carry the deep-module discipline, so that the conventions we are committing to are documented and (where mechanisable) enforced.
35. As the operator, I want to view a pi agent's actions — its tool calls, thinking, and turn events — by tailing pi's RPC/session event stream and/or by having those events surfaced on a bus activity topic in the dash, so that I can watch a headless pi worker without attaching to its terminal.

## Implementation Decisions

- **The protocol is the product (ADR-0041).** The lexicon — record shapes, operations, and convention verb signatures — plus the conformance suite, is the language-neutral contract. The operation index (`methods.json`) becomes operation-lexicons (`query` / `procedure` / `subscription`) that reference the record lexicons; it folds into, or is generated from, the lexicon set (extends ADR-0017).
- **One Go bus, implemented once.** The embedded-NATS bus stays Go and singular, deliberately outside the co-equality rule (ADR-0007/0018/0019). It is the foundation the co-equal clients stand on.
- **The client surface is co-equal per language.** SDK, conventions, and clients are peers across languages; no language is privileged. Sharpens ADR-0022: the locked core is the protocol and the bus; the parallel modules are the language clients.
- **Domain-first tree, single Go module.** Top level is `protocol/`, `bus/`, `clients/<language>/` — organised by what each thing *is*, not by Go visibility. No top-level `pkg/`. Visibility is local: `internal/` nests where hiding is needed. The tree is the index of the parts; `importcheck` enforces the edges (a convention library imports the SDK only, never the bus; the bus never imports clients) — extending the existing production-closure check (ADR-0023).
- **Conventions are lexicon-defined libraries verified by conformance.** Each convention's record types *and* verb signatures live once in the lexicon. Record types are generated per language; verb logic is hand-written — concept, not codegen. A convention verb translates a domain action into a sequence of primitive operations a bare client could also issue (engine-as-library, ADR-0011). Libraries are the default; a request/reply reference-client service is reserved for the rare convention that needs a single writer. Builds on ADR-0004/0034/0035/0039.
- **The conformance suite defines "co-equal."** Recorded primitive-operation transcripts ("given record X, verb V produces exactly these operations") are pure data, replayed by every SDK's tests. A client is co-equal once it passes the vectors for a protocol epoch — the same discipline as abstracting the backend only against a real second one (ADR-0013).
- **No shared frame codec across the server/client line.** The bus stamps frames; each client implements its own codec; all agree via the frame lexicon plus vectors. The one place Go could "cheat" is deliberately closed to force equality.
- **First non-Go client: a TS SDK + a pi extension.** The TS SDK (in `clients/ts/`) is net-new (no current TS SDK exists; pre-cutover TS code was replaced by the rewrite). The pi package `@sextant/pi-bus` is an in-process TypeScript extension that holds the TS SDK `Client` (opened at `session_start`, drained and closed at `session_shutdown`), exposes bus tools (`publish` / `read` / `subscribe` / `unsubscribe` / `artifact_*`) and a `/set-goal` command, bundles a sextant skill, and bridges inbound bus frames into the agent loop via pi's first-party `sendMessage(..., { triggerTurn: true })` primitive (the channel equivalent, proven by pi's shipped `file-trigger` example). The pi client holds its own scoped credentials and speaks NATS over TCP — no WebSocket listener required for pi.
- **Headless workflow sessions are bus clients first.** Primary interaction is bus-addressing: a headless pi worker is a co-equal client, addressed via DM or topic (through the dash or any client). Secondary: a *managed close-and-resume handoff* — on a bus signal the worker cooperatively drains and stops (the Stop/Drain convention), the session persists, the operator resumes it by hand, and the dispatcher re-spawns to resume. Single-owner-at-a-time, coordinated over the bus, so nothing fights for the session.
- **Agent-action observability.** pi's RPC mode streams the agent's events out — message updates (thinking and text), `tool_execution_*` (tool calls), `turn_start`/`turn_end`, `queue_update` — and an extension observes the same set via `pi.on(...)`; sessions also persist as JSONL under `~/.pi/agent/sessions/`. A raw tail of that stream (or the session JSONL) is the floor. The operator-facing path is the pi extension bridging those events onto a sextant bus *activity topic*, so a headless worker's tool calls and thinking are viewable in the dash like any crew member — connecting to the existing "monitor agents from sextant" thread (TASK-151, TASK-150).
- **Deep-module consolidations enabled by the move:** collapse the goals read half and write half into one `conv/goals` module, fixing the `label`/`state` field-drift bug and the proof-filter scope divergence; extract the durable per-subject cursor once; stop the SDK returning an internal wire publish type across its public boundary.
- **The dash is adopted as the first browser client.** After the pi client proves the TS SDK and conventions, the dash becomes a direct NATS-WebSocket co-equal TS client (decided in task-179; built in task-180), bringing the bus WebSocket listener and dash-minted browser credentials into scope and revising ADR-0032/0034. Browser or other-language clients beyond the dash remain deferred until they exist (abstract-only-against-a-2nd-impl).
- **Versioning:** client SDK versions anchor on the protocol epoch; conformance vectors are pinned to an epoch.
- **Sequencing:** (1) mechanical domain-first tree move, no behaviour change; (2) the convention-layer backbone — operation/verb-signature lexicons, the conformance-vector format and runner, the `importcheck` extension; (3) the pi spike, then the TS SDK + `@sextant/pi-bus` (the forcing-function second client); (4) the deep-module content fixes on the clean tree; (5) canon — sign off ADR-0041, add the CONTEXT.md terms, re-file the layout ticket. The Go house-style skill and a curated static-checks gate are revived alongside as the discipline layer.

## Testing Decisions

- A good test exercises external behaviour at the highest available seam, not implementation details: the interface is the test surface (the revived house-style rule).
- **Primary seam (new, highest): the conformance vectors.** Language-neutral recorded operation transcripts that every SDK replays — the keystone for the convention layer and co-equality. Prior art: the existing `methods.json` operation-name conformance test (in the `sextant` CLI and the MCP adapter); extend its pattern from name-set parity to full operation transcripts, run by both the Go and TS suites.
- **The SDK interface seam (existing):** client↔bus behaviour tested through the SDK's public interface. Prior art: the existing Go SDK tests.
- **`importcheck` (existing, ADR-0023):** import-direction enforced via the production dependency closure; extend the rules to the new tree (a convention library imports the SDK only; the bus never imports clients). Prior art: the existing import-discipline check.
- **Real-bus end-to-end (existing `-tags e2e` pattern):** the pi and TS clients validated against a real bus — the production adapter, never a convenience fake (the gate-the-prod-adapter-not-the-fake lesson). Prior art: the existing e2e suite.
- **The pi spike's acceptance criteria are themselves the test:** headless wake (RPC / extension mode), connection survival across `session_start` reasons (reload / fork / resume), back-pressure on a busy topic, and the security/trust posture (bus-delivered instructions versus pi's permission gates; the agent acting on its own scoped credentials, never the operator's). The spike also confirms **agent-action observability**: that the RPC event stream (tool calls, thinking, turn events) is consumable end-to-end, and that those events can be bridged onto a bus activity topic for the dash.

## Out of Scope

- A non-Go **bus** implementation — the bus stays Go, one server.
- Browser or other-language clients beyond the adopted dash client. The dash itself is in scope as a direct NATS-WebSocket TS client, sequenced after the pi client; the bus WebSocket listener and dash-minted browser credentials come with it.
- A declarative convention *engine* that interprets verb logic from the lexicon — rejected (engine-in-disguise risk); verb logic stays hand-written.
- Versioned or certified capability profiles — rejected as over-built; opt-in is link-time plus an advisory, unverified hint.
- Codegen of verb *logic* — only record *types* are generated.
- Reworking the bus's transport, identity, or presence — unchanged.
- The remaining deep-modules candidates from the assessment beyond the convention-layer consolidations named here — separate follow-ups.

## Further Notes

- Rests on [ADR-0041](../../docs/adr/0041-clients-are-co-equal-across-languages.md) (proposed) and ADRs 0004, 0011, 0017, 0022, 0028, 0034, 0035, 0039.
- The layout move precedes the deep-module content so each is independently reviewable; the move is mechanical (rename plus import-path rewrite), zero behaviour change. Import paths only get more expensive to change as adoption grows, so doing it before a TS SDK and external importers cement them is deliberate.
- pi deep-dive findings (sources: pi.dev, github.com/earendil-works/pi, the shipped `file-trigger` example): pi is an MIT, TypeScript, Node/Bun/Deno agent harness; extensions are in-process TS modules exposing tools and skills; the inbound-wake primitive (`sendMessage` / `triggerTurn`) is documented and shipped; a package can hold scoped credentials, a long-lived TCP connection, and background work. Useful prior-art packages: `file-trigger` (the wake template), `pi-intercom` / `pi-messenger` (inbound-message-wakes-agent), `pi-link` (a live socket across a session), `pi-subagents` (event-wakes-parent), `pi-schedule-prompt` / `pi-heartbeat` (keepalive). There is no NATS pi package and no maintainer-blessed bus integration yet — the spike de-risks this.
- The pi handoff (managed close-and-resume) is secondary; bus-addressing is the primary and required path.
