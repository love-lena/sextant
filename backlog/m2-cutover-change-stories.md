# M2 cutover — change stories

*A review narrative for the #76–#85 stack. Built AFK; stacked for review,
nothing merged. Read top-to-bottom — each PR is one beat in a single arc.*

## The thesis

One sentence: **Sextant becomes the bus.** Every protocol operation now
flows through one process that serves it against a pluggable backend,
stamps the frame, and enforces the author — and after the last slice,
nothing reaches the backend any other way. The payoff is the keystone
property of ADR-0019: the `author` on every message and artifact is
**unforgeable**, because the only way to write anything is to ask the bus
over a call made under your own authenticated identity.

This is the story of how the stack gets there in ten green, stacked
steps. The DAG is mostly a straight line; the one fork is the CLI (#82),
which branches off the SDK and rejoins conceptually but not in git.

```
#76 frame ─ #77 backend ─ #78 bus-serves ─ #79 identity ─ #80 publish ─ #81 list ─┬─ #82 CLI
                                                                                   └─ #83 data-plane ─ #84 connect ─ #85 allow-list ★
```

★ = the security keystone. If you only review two PRs, review #78 (where
the bus starts stamping the author) and #85 (where the credential makes
that stamp unforgeable).

---

## Part I — The foundation

### #76 · `feat/m2-frame` — one envelope shape for everything
*10 files, +286/−280.*

**The change.** The wire type `Envelope` becomes `Frame`; its `sender`
field becomes `author`; messages and artifacts now share one frame shape
(artifacts carry the extra `revision` / `createdAt` / `updatedAt`
fields). **Why.** A single at-rest representation is what later lets the
bus store an artifact as "a frame" and a message as "a frame" through the
same backend seam — the coupling that makes the artifact cutover (#83) a
single unit rather than a special case. Renaming `sender → author` is the
vocabulary the whole cutover hangs on: the bus *authors* the stamp.

**Flag.** This is the widest-touching rename in the stack (10 files) but
purely mechanical; the risk is entirely "did the rename miss a spot,"
which the compiler and the wire round-trip tests answer.

### #77 · `feat/m2-backend-iface` — the backend as a narrow interface
*5 files, +725.*

**The change.** A new `internal/backend` package defines the substrate
the bus runs on: a durable ordered log (`Append`/`Read`/`Subscribe`) plus
a versioned KV (`Create`/`Put`/`CompareAndSet`/`Get`/`Delete`/`Watch`/
`Keys`), with sentinel errors so the bus never imports a backend's error
types. A NATS module satisfies it; a conformance suite pins the contract.
**Why.** This is the ADR-0018 invariant made real: *swap the backend ⇒
the protocol is unchanged.* Each method was shaped against "how would
Redis satisfy this?" so the seam stays backend-portable rather than
NATS-shaped. The backend never parses a frame — it stores bytes and
hands back revisions; all frame semantics live in the bus.

**Flag.** The interface is the most consequential design surface in the
whole rebuild. Worth the closest read: is anything here secretly
NATS-shaped (opaque cursors, the watch change-stream) in a way Redis
couldn't honor? The conformance suite is the contract; if you distrust a
method, distrust it there.

### #78 · `feat/m2-bus-serves` — the bus answers calls ★
*5 files, +686/−1.*

**The change.** The bus subscribes to a call space and serves the
request/reply operations — `message.publish`, `message.read` (cursor
pull), the four artifact ops, `clients.list` — against the backend. It
**stamps the frame**: the id (a ULID), the author (from the call's
subject token), the kind, the epoch, and for artifacts the revision and
timestamps. Bounded concurrency, no head-of-line blocking, reply after
the append is durable. `internal/wireapi` defines the call subject scheme
and the per-op request/response shapes shared by bus and SDK.

**Why this is the hinge.** This is where "the bus stamps the author"
becomes true. From here the author is *trustworthy* (the bus sets it, not
the client) but not yet *unforgeable* (a client could still, at this
point, write the backend directly or call under another id). #85 closes
that gap. Everything between is moving callers onto this path.

---

## Part II — Identity, then the SDK moves onto calls

### #79 · `feat/m2-identity` — the id is a bus-minted ULID
*11 files, +222/−100.*

**The change.** A client's primary identity stops being the human name it
picked and becomes a **ULID the bus mints**, carried as the credential's
authenticated name; the human label rides alongside as a non-keying
`display_name` attribute. **Why.** An identity the *bus* owns is an
identity a client cannot forge or collide on — the precondition for
scoping a credential to it (#85) and for the author stamp meaning
something. It also kills the old silent-collision footgun where two
clients picking the same name shared a registry key.

**Flag.** This changes what `clients.list` returns and how the registry
is keyed; watch that nothing downstream still assumes "id == the name I
typed." (The CLI in #82 surfaces `display_name` for humans precisely
because the id is now an opaque ULID.)

### #80 · `feat/m2-sdk-client` (5a) — Publish becomes a call
*4 files, +94/−13.*

**The change.** `Client.Publish` sends a `message.publish` **call**
instead of writing the stream directly; new `Client.FetchMessages` pulls
a batch via `message.read` (cursor + resume — the pull complement to
`Subscribe`). **Why.** First SDK method across the line. Small on
purpose: it proves the call path end-to-end from the library before the
riskier slices lean on it.

### #81 · `feat/m2-clients-call` (5b) — ListClients becomes a call
*4 files, +66/−85.*

**The change.** `Client.ListClients` goes through the `clients.list`
operation. Because the bus now reads the whole registry on every client's
behalf, a single corrupt record **skips quietly** rather than failing the
listing for everyone, and each id is sourced from its authoritative
registry key. **Why / note the deliberate behavior change.** The old
SDK-direct read failed loud on a corrupt record because the reader *was*
the client and a corrupt record meant *its own* view was broken;
serverside, one client's bad record shouldn't blind every other client's
directory. That's a real semantics shift — called out so it's a decision,
not an accident.

### #82 · `feat/m2-cli` (PR6) — the operator/test CLI + conformance
*7 files, +544/−1.*

**The change.** An operator/test CLI with op-name parity (the verbs match
the protocol operations one-for-one) plus a conformance test that pins
that parity. Smoke-verified the full M2 loop end-to-end — two clients
exchange a message and compare-and-set an artifact through the bus — and
**recorded it as a VHS demo** (`docs/demos/`). **Why.** The conformance
test is half the acceptance spine (the M2 DoD e2e in PR8 is the other
half). The CLI is also the first *human* on the call path, which is how
the loop got smoke-tested before the SDK cutover finished.

**Note on the DAG.** #82 branches off #81, parallel to the cutover
branch (#83). It's reviewable independently; it does not depend on the
push-stream or connect work.

---

## Part III — The cutover proper

### #83 · `feat/m2-cutover` (5c) — push streams + the artifact unit
*8 files, +580/−141.*

**The change.** The bus serves `message.subscribe` and `artifact.watch`
as **server-side relays** into a client's private delivery space
(`sx.deliver.<clientID>.<subID>`), each ended by a `subscription.stop`
control op or by bus shutdown (one root context cancels them all). The
SDK's `Subscribe` / `WatchArtifact` **and all four artifact methods** move
onto calls. **Why the artifacts move as one unit.** The bus stores an
artifact as a frame at rest; the old SDK-direct path stored a raw record.
You cannot half-cut that over — read and write must agree on the at-rest
shape — so create/update/get/delete/watch flip together.

**Flag (deferred gap, by design).** Crash-driven relay teardown — a
client that never sends `subscription.stop` — is deferred to the liveness
work (TASK-20), the *same* gap the clients registry already has. A lease
would be a second liveness subsystem; better to solve liveness once. This
is a known, bounded leak, not an oversight.

### #84 · `feat/m2-connect-cutover` (5d-i) — the handshake through the bus
*9 files, +222/−113.*

**The change.** Two new internal ops: `clients.register` (the bus writes
the directory record, keyed by the caller's authenticated id, stamped
with the bus clock — and **folds the protocol-epoch hard-gate**: it
returns the bus epoch, which the SDK exact-matches, plus the bus-stamped
`connected_at` for the clock-skew announce) and `clients.deregister` (on
`Close`). Cooperative **drain now delivers over each client's own push
space** (`sx.deliver.<id>.drain`) instead of a control broadcast, so a
client needs no permission beyond its own delivery subscription to
receive it. The SDK's `Client` drops its backend handle entirely.

**Why fold the epoch gate into register.** It keeps the handshake one
round-trip and puts the gate where the authority is — the bus tells the
client the epoch; the client doesn't read it from shared state. After
this PR the SDK touches the backend *nowhere*. Only the credential's
deny-only permissions still technically allow direct access; #85 removes
even that.

### #85 · `feat/m2-allowlist` (5d-ii) — the unforgeable author ★
*11 files, +233/−145.*

**The change.** Each minted credential flips from a shared **deny-list**
to a per-client **allow-list** scoped to its bus-minted ULID:

```
Pub.Allow = [ sx.api.<id>.>                      ]  # only its own calls
Sub.Allow = [ sx.deliver.<id>.> , _INBOX.<id>.>  ]  # own deliveries + own inbox
```

**Why this is the keystone.** The subject token a client publishes a call
under is now *exactly* the identity that was authenticated. So the author
the bus stamps from that token cannot be forged — there is no subject a
client may publish to that would let it call as another id or write the
stream / buckets / control space directly. Allow-list, not deny-list:
everything is denied unless named, so there is no lifecycle to squat and
no operator state to reach. This is the sentence the whole stack was
written to earn.

**Two decisions worth your eye:**

1. **A per-client inbox (not the shared `_INBOX.>`); `Resp` omitted.** A
   client's own request receives the bus's reply on its inbox, so the inbox
   must be subscribable or every call times out. The first cut allowed the
   shared `_INBOX.>` — and an **independent adversarial review caught a
   [High] gap**: with the wildcard, any client could subscribe `_INBOX.>`
   and passively receive *every other* client's call replies. Forgery was
   never possible (a client can't publish to an inbox to spoof a reply), but
   reply *confidentiality* wasn't held. Fixed in the follow-up commit with a
   **per-client inbox**: the credential allows only `_INBOX.<id>.>`, the SDK
   sets a matching `nats.CustomInboxPrefix`, and a regression test pins that
   a client cannot subscribe the shared/foreign inbox. `allow_responses`
   (`Resp`) is *not* needed — the client is a requester, never a responder —
   and is deliberately left out. The review also hardened the id→permission
   path with a fail-loud guard for any future non-ULID id. (Full review +
   resolution are on the PR.)

2. **Operator-side write seams (resolved in review — no production
   surface).** Because clients now have *no* direct backend access, the only
   writes that can set up a different epoch, a hand-seeded or corrupt
   registry record, or a raw frame that bypasses stamping are the bus's own
   — so the fail-loud and quarantine tests need a seam to those. The first
   cut put four methods on the production `*Bus`; review (rightly) wanted
   them off it. They now live in `pkg/bus/export_test.go` (package bus, but a
   `_test.go` file — reaches the unexported backend, **absent from every
   production build**), and the five tests that use them moved to
   `package bus_test`, which can both see `export_test.go` and import
   `pkg/sextant` to drive the real SDK (external test packages may import
   their importers). No build tag, so a bare `go test ./...` still compiles.
   The general rule is captured in `docs/conventions/test-features.md`.

**Test rework that the flip forced.** The deny-only suite got away with
publishing calls under arbitrary subject tokens; the allow-list rejects
them, so the bus tests now thread the *minted ULID* as the call subject —
this was the non-obvious breakage. Tests that used to poke the backend
through a client's handle now use the operator seams or read through
`ListClients`. `TestClientCanWriteConventionBuckets` was deleted (its
premise — clients write KV directly — is now false by design) and
replaced with `TestClientRegistersViaCall` (the positive shape).

---

## What's true after the stack

- Every protocol operation is served by the bus against the backend
  interface; the SDK and CLI never touch the backend.
- The bus stamps every frame's id, author, kind, and epoch (and artifact
  revision/timestamps), and the author is **unforgeable**.
- Backend-portability is a tested contract (#77 conformance), so the
  ADR-0018 invariant holds: swap the backend, the protocol is unchanged.
- The acceptance spine's first half (conformance, #82) is in; the second
  half (the M2 DoD e2e) lands with PR8.

## Open flags carried forward (not blockers)

1. **Crash-driven teardown** of relays and registry entries → TASK-20
   liveness (one subsystem, not a per-feature lease).
2. ~~Operator write-seams placement~~ — **resolved in review**: moved off the
   production type into `export_test.go` + `package bus_test`, no build tag
   (see #85 and `docs/conventions/test-features.md`).
3. **`clients.list` corrupt-record robustness** is now skip-quietly, not
   fail-loud — a deliberate serverside shift (see #81).
4. **Drain semantics** — the SDK signals `Drained()` and best-effort
   leaves the registry rather than exiting the process from a library;
   ADR-0010 frames it as "ending the client," flagged there for review.

## Still to build (beyond this cutover)

- **PR5.5** — artifact ULID-addressing + `artifact.list` (the §3 artifact
  half; `methods.json` addresses by name today, id tomorrow).
- **PR7** — MCP server + channel + skill (the Claude Code plugin).
- **PR8** — ergonomics (`run`, `up --with-dir`, per-client creds,
  `--reclaim`) + getting-started + the M2 DoD e2e walkthrough.
