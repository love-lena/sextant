---
status: accepted
signed_off_by: lena
date: 2026-06-04
---

# Sextant

The shared language for Sextant — a protocol and SDK for AI agents to
communicate and collaborate over a bus. Definitions only: no architecture, no
implementation. For *why*, see `docs/adr/`; for *how to work here*, see
`AGENTS.md`.

## Language

**Client**:
A process that speaks the Sextant protocol — the universal unit. A harness,
monitor, dispatcher, human UI, or workflow coordinator is each a client.
_Avoid_: participant, service, node, agent (when you mean the process)

**Principal**:
The one human's client, per bus, that is the root of authority — the identity
whose messages other clients act on as their own operator's direct input (the
trust model is ADR-0030). **Claimed frictionlessly, re-pointed deliberately**
(ADR-0031): the first human seat to self-enroll claims the still-unclaimed
designation with no extra command, while re-pointing an *established* principal
is operator-only and takes an explicit `--force`. The **human-only** guarantee
holds at the source — the bus refuses to claim the principal for an agent seat,
so auto-minting agents discover and adopt the principal but can never become it.
An opinionated **extension** over the locked core, not a core-protocol concept —
the universal protocol stays principal-free and open among authenticated clients
(ADR-0012, ADR-0022).
Distinct from the bus-owner **Operator** tier (ADR-0012/0015) and from a plain
**Client**.
_Avoid_: super user, sudo, owner; operator (that is the bus-owner credential
tier, a different concept)

**SDK**:
The library you build a client with.
_Avoid_: client library

**Co-equal client**:
A client implementation that is a peer of every other, in any language — none
privileged (ADR-0041). The **bus** is one Go server, implemented once; the client
surface (**SDK** + **conventions layer**) is co-equal across languages. A client
is co-equal for a protocol **epoch** once it passes the **conformance suite** for
that epoch — the same wire behaviour, not a look-alike. The Go and TypeScript
SDKs are co-equal peers.
_Avoid_: reference SDK, primary/secondary client, port (the TS SDK is not a port
of the Go one — both conform to the protocol)

**Conformance suite**:
The recorded, language-neutral transcripts that **define** when a client is
co-equal: given a record and a **convention** verb, the exact ordered primitive
**operations** it must emit — and, at the wire level, the exact **frame** bytes.
Every **SDK** replays the same vectors; passing them for an **epoch** is what
makes a client co-equal. The vectors are data, not code — pure transcripts under
`protocol/conformance`.
_Avoid_: test suite (it is the cross-language contract, not one language's tests),
golden files (it is shared, not per-language)

**Conventions layer**:
The patterns built *on* the primitives that are not core — a **goal**, the
**assistant**, review-state, **`relates`**, the **workflow** contract. Each is a
**lexicon-defined library** over the **SDK**: its record types and verb signatures
live once in the lexicon (record types generated per language; verb logic
hand-written — concept, not codegen), and the library issues only **operations** a
bare client could, never reaching the **bus** internals (enforced mechanically).
Optional and forkable; the bus stays primitive and content-opaque. The reference
conventions claim their lexicon namespace at the auth level (`goal`, `review`,
`assistant`, …) — the opinionated "this is what a goal looks like" — while a fork
defines its own (`mygoal`). They sit in their own promoted tier above clients,
never in the locked core ([ADR-0049](docs/adr/0049-clients-conventions-and-tools.md)).
_Avoid_: framework, engine (verb logic is a library, never a bus-interpreted
engine), middleware

**Harness plugin**:
A **client** that makes an existing agent runtime — a coding agent, an editor — a
first-class bus client: its own scoped identity, bus tools, often a bundled skill.
A *role* a client plays, not a separate tier; the Claude Code plugin and `pi-bus`
are harness plugins (one rides the Go MCP client, the other the TypeScript
**SDK**). See [ADR-0049](docs/adr/0049-clients-conventions-and-tools.md).
_Avoid_: integration, extension, bot

**Tool**:
Build- or dev-time code that reads the protocol or **SDK** and emits output —
generated docs, generated types — and never connects to the **bus**. Not a
**client**: it holds no identity. `docgen` is a tool.
_Avoid_: calling a tool a client; "script" for a tool that is part of the build

**Bus**:
What clients connect to in order to reach each other and their shared work — the
sole access point, which implements the protocol's operations. It runs on a
pluggable **backend**.
_Avoid_: broker, server, queue, backend (the backend is what the bus runs on)

**Backend**:
The pluggable stream substrate the bus runs on, behind one internal interface;
swappable (a different module per substrate) without changing the protocol.
Opaque to clients.
_Avoid_: naming a specific backend in client-facing docs; "batteries" as the
formal term (only the embedded-convenience framing)

**Message**:
A typed record published on a subject, for events and conversation.
_Avoid_: event (when you mean the frame), packet

**Topic**:
A named subject many clients publish to and subscribe to. A naming
convention over the messages space, not a bus construct — it has no registry,
membership, or access control.
_Avoid_: channel (reserved for the Claude Code harness push mechanism), room,
group

**Artifact**:
A named, versioned unit of durable shared work, owned by one author at a time
(a plan, a review, a result).
_Avoid_: document, file, blob, state

**Record** / **Lexicon**:
The typed content a **frame** carries — user space. Its type is a *lexicon* —
the schema, named on the record itself.
_Avoid_: payload, body

**Frame**:
The bus-stamped wire wrapper around a record — id, kind, epoch, author. The
**record is user space; the frame is bus space** (the bus produces it, not the
client). `kind` discriminates a frame: a **message** in flight, an **artifact**
at rest.
_Avoid_: envelope (renamed), wrapper, header

**Operation**:
A domain action the bus implements and a client invokes — publish, read, the
artifact operations, listing clients. The set of operations is the protocol's
surface.
_Avoid_: verb, method, command, RPC

**Call**:
A client invoking an operation on the bus and receiving its result — the
client↔bus path.
_Avoid_: request/reply (that is client↔client)

**Request / reply**:
A client↔client exchange: one client sends a request to another and gets a reply.
Distinct from a **call** (client↔bus).
_Avoid_: using "request/reply" for the client↔bus call

**Clients registry**:
The durable, bus-maintained directory of issued client identities — written when
the bus issues an identity, removed only by retire; it survives disconnect and bus
restart and lists offline clients too (ADR-0020).
_Avoid_: presence (that is the read-time liveness *view* over the registry, not
the registry itself), service discovery

**Context**:
A saved (bus URL + identity + creds) profile a client install keeps under a
local name, so commands need no connection flags once one is active — the
kubectl/`nats context` pattern. Client-side and local; not a bus construct. Its
name is a handle you choose (at register time it defaults to the display name),
distinct from the identity's bus-minted ULID and its non-unique display name.
_Avoid_: profile, account, session, environment

**Channel**:
The Claude Code harness push mechanism (a research-preview feature): the
plugin adapter (`sextant-mcp`, ADR-0028) declares it and pushes inbound bus
messages into the session as `<channel>` events. A harness construct, never a
bus one — the bus-side concept is the *topic*.
_Avoid_: using "channel" for anything bus-side

**Workflow**:
A reusable, generic multi-step process — a `WORKFLOW.md` template of trigger and
steps (ADR-0048), driven by a coordinator client. Carries no goal or criterion of
its own; the binding is made when a **run** is spawned. One workflow, many runs.
_Avoid_: pipeline, job, DAG (shapes a workflow may take, not the thing itself);
using "workflow" loosely for any scenario — that is a *use case*, not a Workflow;
conflating a workflow (the reusable template) with a **run** (one instance)

**Run**:
One live instance of work (ADR-0048) — a ULID with steps, the criteria it works
**toward**, and its **stop conditions** — held as the `sextant.workflow.run/v1`
state artifact. It comes from a **workflow** template or is **ad-hoc** (a one-off
mobilize, `template: null`); either way it is what the operator watches.
Discoverable as a typed artifact with a live status; identified by ULID + what it
does, never a persona. Its stop conditions are an additive, disjunctive set of
prompts — meet any one, at least one required — so a run never halts without
posting the brief that justifies it.
_Avoid_: job, task, session; conflating a run (one instance) with the **workflow**
(the reusable template)

**Coordinator**:
The client that drives a **run**'s steps and records its progress.
_Avoid_: orchestrator, manager, controller

**Dispatcher**:
A client that turns spawn requests into running clients.
_Avoid_: scheduler, supervisor (it launches; it never supervises)

**Goal**:
A shared objective the crew works toward — a **north-star** plus its acceptance
**criteria** — held as the latest-value artifact `goal.<id>`. Its **status is
derived** from the criteria rollup (how many are met of the total), never stored
as its own field. A record convention over the artifact operations, not a core
concept; distinct from a **client's** `agent.status` (what one client is doing
now) — a goal is an outcome many clients move.
_Avoid_: task, milestone, project; storing a goal-level state (it is derived)

**Criterion**:
One acceptance condition of a **goal**, embedded in the goal record as
`{id, text, status, owner?}` — `status` ∈ `met · in-progress · waiting-on-you ·
blocked · not-started`. A *met* criterion has at least one **proof**-kind related
artifact (the invariant). Who marks it met is fuzzy and uncoded — an agent that
ran a check, an inference from a human-approved artifact, or the operator
directly; no gate is baked in.
_Avoid_: acceptance test, requirement, subtask

**`relates` (artifact convention)**:
An optional field a **record** may carry to associate an **artifact** with a
**goal** or **criterion**: `relates: [{goal, crit?, kind}]`, where `kind` is
`proof` (evidence backing a met criterion), `related` (default; a generic
association), or `toward` (a **run** working toward a criterion; ADR-0048). A
criterion's proof/related/toward sets are **projected** from these
declarations — read from the artifact side, never written onto the criterion.
Content-opaque to the core; a convention, not a schema change.
_Avoid_: link, dependency, parent/child

**Needs-review (review-state default)**:
An artifact's `review` block (ADR-0034) defaults to **neutral** — a new artifact
is not awaiting the operator. A producer (agent, workflow, or operator) sets
`review.state = review` **explicitly** when the artifact is for the operator's
judgment (brief, proposal, design, decision-doc) — the *intent* half, settable
by anyone, no rev. The operator's *verdict* (`by/at/rev`) is server-set by the
review endpoint on approve/changes. `review.state` is ONE enum: the producer
intent `review` plus the operator verdicts `approved`/`changes`/`rejected`/
`archived`, discriminated by `by/at/rev` presence. Context / working / done
stay neutral.
Home/inbox surfaces only `review`-state artifacts (+ criteria-waiting +
question-messages). A **norm, not enforcement** (signal-not-manage).
_Avoid_: default-to-review, auto-flag

**`goal.update` (the goals stream)**:
A signalled transition on a **goal**, published on the topic `msg.topic.goals` —
an observation that a goal moved (a criterion met, a goal opened), not the goal's
value. It **signals; it never manages**: the owner and operator stay
authoritative and nothing gains authority over a **client**. Mirrors the
current-value-artifact + event-stream pairing the **workflow** harness uses.
_Avoid_: command, assignment, directive

**Assistant**:
The operator's own **client**, run as an agent — one identity with two duties:
it **answers** (read-only) when the operator messages it, reading the workspace
to reply on the operator's DM; and it **defends** the operator's attention by
curating the Home/inbox projection so "Needs you" holds only the real calls with
one clear top action. A **convention**, not a core concept (ADR-0039): a role
prompt plus the swappable `assistant` designation artifact, over the existing
client, artifact, and message operations — no new operation, no privileged tier,
no operator authority. **Signal-not-manage**: it curates the *view*, never a
review verdict or an owner's state. The reference assistant is named **violet**.
_Avoid_: bot, secretary, manager; an assistant that *acts* on the operator's
behalf (it answers and curates a projection; it never acts)

**`assistant` (designation artifact)**:
The latest-value artifact **`assistant`** that names which live **client** is the
current **assistant**: `{client_id, name, accent}` (its bus identity, display
name, and identity colour). The dash and crew read it to know who the assistant
is and how to reach it; re-pointing it swaps the assistant with no code change.
Mirrors the `goal.<id>` / `status.<id>` latest-value pattern; a convention, not a
schema change.
_Avoid_: hardcoding the assistant's identity; registry, account

**Epoch**:
The protocol version; a client checks it on connect.
_Avoid_: version, schema version

**Ephemeral** / **Canon**:
*Ephemeral* is the agent workspace (specs, plans), never committed. *Canon* is
the signed-off, committed docs. committed ⇔ signed-off.
_Avoid_: draft, scratch

**Stop** / **Drain**:
A client cooperatively shutting itself down on a signal.
_Avoid_: kill (reserve that for forcing a process from the outside)

## Relationships

- A **bus** carries many **clients'** **messages** and holds their **artifacts**.
- A **client** publishes and subscribes to **messages**, and reads and writes
  **artifacts**.
- Every unit is one of a **client** (a process with an identity), a **convention**
  (records and verbs, no identity), or a **tool** (reads and emits, never
  connects). Where a directory is two, it splits — the convention up to the
  conventions tier, the process down to a client
  ([ADR-0049](docs/adr/0049-clients-conventions-and-tools.md)).
- A **run** is one instance of a **workflow** (or ad-hoc), driven by a
  **coordinator**; a **dispatcher** spawns new **clients**. Both coordinator and
  dispatcher are just clients.
- A **goal** holds its **criteria**; its status is derived from their rollup. An
  **artifact** declares (via `relates`) which goal/criterion it backs as **proof**,
  is generically **related** to, or works **toward**; a **run** is one in-flight
  "how" toward a criterion. Movement is signalled on the `goal.update` stream — it
  reports, it never directs a **client**.
- The **clients registry** lists every issued client; **presence** is its liveness
  view — who is alive right now, derived at read time from the bus's live
  connection table OR a fresh client **heartbeat** (`last_seen`), so a client
  behind a leaf node — invisible to the connection table — still reads online
  while it beats ([ADR-0036](docs/adr/0036-presence-and-liveness-derive-from-a-client-heartbeat.md)).
- A **client** makes a **call** to invoke an **operation** on the **bus**; the bus
  stamps a **frame** around the record and stores or relays it via the **backend**.
- A **bus** has at most one **principal** (a human's client), designated at
  bootstrap by the operator and **bus-enforced**; other clients discover and
  adopt it. It is an opinionated extension over the core — the universal protocol
  has no principal. A client that is not the principal is just a **client**;
  there is no separate role for the trusting side.
- The **assistant** is a **client** named by the latest-value `assistant`
  artifact. It **answers** the operator (read-only) and **defends** the
  operator's attention by curating the Home/inbox projection over the
  `review`-state **artifacts**, goal criteria, and question-**messages** in the
  pool — it curates the *view*, never an owner's state (a convention, ADR-0039).

## Dev-dash loop

Run a dev `sextant-dash` on a free port alongside the managed dash — no swap,
no taking prod down:

```sh
sextant-dash --port 0 --ui <worktree>/clients/go/apps/internal/dashapi/web/app
```

Key points:
- Use the **`sextant-dash` binary** directly. `sextant dash` no longer serves
  after the binary split (ADR-0046).
- `--port 0` picks a free port; the managed prod dash stays on `:8765`.
- `--ui <dir>` serves the SPA from disk — **no Go rebuild** needed for
  UI-only changes. For Go-side changes, rebuild the binary.
- Two servers coexist because `sextant-dash` holds no standing bus connection
  (connects to mint, then closes) and each browser tab is a co-equal client.

## Flagged ambiguities

- "presence" once named the registry itself — resolved: the thing is the
  **clients registry**; "presence" is only its liveness view.
- "participant" drifted against the running process — resolved: the term is
  **client**.
- "client library" vs the process — resolved: the library is the **SDK**; the
  process is a **client**.
- "channel" named both a bus room and the Claude Code harness push mechanism —
  resolved: the bus concept is a **topic**; "channel" is reserved for the
  harness mechanism (ADR-0017).
- "envelope" for the wire wrapper — resolved: it is a **frame** (record = user
  space, frame = bus space).
- "verb" for a protocol action — resolved: an **operation**.
- "face" for the ways to reach the bus (SDK / Wire API) — considered and
  dropped: no collective noun; they are just how a client speaks the protocol.
- "agent" for a participant or a direct-address target — resolved: the universal
  term is **client** (direct addressing names a client, not an "agent").
- "request/reply" used for the client↔bus path — resolved: request/reply is
  **client↔client**; the client↔bus path is a **call** (ADR-0018).
