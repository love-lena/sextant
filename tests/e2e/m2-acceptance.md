# M2 acceptance — the collaboration loop (executable spec / DoD e2e)

**This is the target the implementing agent codes against.** It is written
*test-first*: today it FAILS (the ADR-0020 identity surface isn't built, and the
stack isn't merged). When M2 is complete — the #76–#85 cutover **plus** the
ADR-0020 identity model (plus PR5.5 / MCP / ergonomics) — running this scenario
produces the **expected transcript** below (modulo the normalizations noted), and
the per-step **asserts** all hold. That match *is* "M2 done."

It is also the single source of truth for the **VHS demo**: the same ordered steps,
two clients in two panes (alice, bob) over one bus, render the GIF. The transcript
here is what the panes show.

> **Status:** RED until ADR-0020 + the stack land. Not wired into the green CI gate
> yet; it becomes PR8's DoD e2e. Lines marked **[PROPOSED]** pin a CLI/output
> surface ADR-0020 does not specify — they are this spec's design proposal, for
> review. Everything else is grounded in the existing CLI (#82).

## Normalizations (the diff masks these before comparing)

- ULIDs → `<ULID>` (ids, authors). Distinct ids that must *differ* are written
  `<ULID:alice>`, `<ULID:bob>`, `<ULID:msg>` — the harness asserts equality/
  inequality on these, not their literal value.
- RFC3339 timestamps → `<TS>`. Bus URL/port → `<URL>`. Temp paths → `<PATH>`.
- `clients list` / `read` order is sorted (by id), so output is deterministic.

## Scenario

One bus; two clients, **alice** and **bob**, each its own process/pane. The loop
exercises: enrollment (both auth modes), a message with an unforgeable author, a
shared artifact via compare-and-set, the live directory with presence, durable
identity across reconnect, and retire.

### 0 — bus up  (pane: bus)

```
$ sextant up --store <PATH>
sextant bus up
  url:        <URL>
  discovery:  <PATH>/bus.json
  ...
```
**asserts:** bus is listening; discovery file written.

### 1 — alice is issued by the operator; bob self-enrolls  **[PROPOSED]**

alice is minted *by the operator* (held-identity mode — the human at the terminal
is the operator), bob *self-enrolls* on the same box (bootstrap/locality mode). Both
are `clients.register`; the difference is only how the request is authorized
(ADR-0020).

```
# pane: bus (operator)            — held-identity mode: mint for another
$ sextant clients register alice --kind worker
registered alice as <ULID:alice>            # [PROPOSED] output shape
  creds: <PATH>/alice.creds

# pane: bob                       — bootstrap/locality mode: mint for self
$ sextant clients register --self --kind reviewer
enrolled as <ULID:bob>                       # [PROPOSED]
  creds: <PATH>/bob.creds
```
**asserts:** two **distinct** bus-minted ULIDs (`<ULID:alice>` ≠ `<ULID:bob>`);
neither the operator nor bob ever touched the signing keys (keys stay in the bus);
bob obtained an identity with no pre-existing credential (enrollment).

### 2 — bob subscribes; alice publishes; the message arrives with an unforgeable author

```
# pane: bob                       — long-running subscriber
$ sextant subscribe msg.topic.plan --creds <PATH>/bob.creds
subscribed to msg.topic.plan (Ctrl-C to stop)        # stderr

# pane: alice
$ sextant publish msg.topic.plan '{"hello":"world"}' --creds <PATH>/alice.creds
published to msg.topic.plan

# pane: bob  — the delivery appears live
[msg.topic.plan] <ULID:msg> <ULID:alice> {"hello":"world"}
```
**asserts (the keystone):** the frame bob receives has **author == `<ULID:alice>`**,
the bus-stamped id of the *publisher* — not a value alice chose. alice cannot
publish a frame whose author is bob: an attempt is denied by the per-client
allow-list (the unforgeable-author guarantee, #85).

### 3 — a shared artifact, via compare-and-set

```
# pane: alice
$ sextant artifact create the-plan '{"title":"v1"}' --creds <PATH>/alice.creds
the-plan now at revision 1

# pane: bob — update at the revision it last saw
$ sextant artifact update the-plan '{"title":"v2"}' --rev 1 --creds <PATH>/bob.creds
the-plan now at revision 2

# pane: alice — a stale update is rejected (CAS)
$ sextant artifact update the-plan '{"title":"v3"}' --rev 1 --creds <PATH>/alice.creds
sextant: artifact "the-plan" changed since revision 1        # stderr; non-zero exit

# pane: alice — read back
$ sextant artifact get the-plan --creds <PATH>/alice.creds
the-plan (revision 2)
{"title":"v2"}
```
**asserts:** create→rev 1; bob's update→rev 2; the stale (rev 1) update is
**rejected**; get shows rev 2 with bob as the stamped author.

### 4 — the live directory shows presence  **[PROPOSED: the presence column]**

```
# pane: alice
$ sextant clients list --creds <PATH>/alice.creds
<ULID:alice>  alice                 worker      epoch=1  online
<ULID:bob>    bob                   reviewer    epoch=1  online
(2 clients)                                                          # stderr
```
**asserts:** both registered clients listed, sorted by id, each **online** — and
presence is *derived from the live connection*, not from a register/deregister call
(ADR-0020).

### 5 — durable identity across disconnect/reconnect  **[PROPOSED]**

```
# pane: bob — stop the process (Ctrl-C / kill), then, shortly after, on alice:
$ sextant clients list --creds <PATH>/alice.creds
<ULID:alice>  alice                 worker      epoch=1  online
<ULID:bob>    bob                   reviewer    epoch=1  offline      # was online
(2 clients)

# pane: bob — reconnect with the SAME creds
$ sextant subscribe msg.topic.plan --creds <PATH>/bob.creds
subscribed to msg.topic.plan (Ctrl-C to stop)

# pane: alice
$ sextant clients list --creds <PATH>/alice.creds
<ULID:bob>    bob                   reviewer    epoch=1  online       # same <ULID:bob>
...
```
**asserts:** bob's record **persists** while disconnected (still listed, `offline`)
— not reaped; on reconnect it is the **same `<ULID:bob>`** flipped back `online`.
Identity is durable; presence is the connection.

### 6 — retire decommissions the identity  **[PROPOSED]**

```
# pane: bus (operator)
$ sextant clients retire <ULID:bob>
retired <ULID:bob>

# pane: alice
$ sextant clients list --creds <PATH>/alice.creds
<ULID:alice>  alice                 worker      epoch=1  online
(1 clients)
```
**asserts:** retire **removes the identity for good** (gone from the directory),
distinct from a disconnect (which only goes offline). A clean client `Close` does
NOT retire.

## How this becomes the runnable test (for the implementer)

A small harness (Go `tests/e2e` driving the built `sextant` binary, or a shell
script + golden) that: starts a bus in a temp store; runs the steps above, each
client a child process (bob's subscriber backgrounded, its stdout captured); pipes
all output through the normalizations above; and diffs against the golden
transcript. `-update` regenerates the golden. Keep it behind an `e2e` build tag (or
`tests/e2e/run.sh`) so it is runnable on demand but out of the default green-CI gate
until it passes — at which point it joins CI as the M2 DoD e2e.

Verify the harness plumbing against the *existing* loop first (steps 2–4 work today
against the #82 CLI with `sextant token` in place of step 1's `register`), so the
only thing red is the genuinely-unbuilt ADR-0020 surface (steps 1, 4-presence, 5,
6) — not harness bugs.

## Open design choices this spec pins (review these)

1. **`clients register` CLI shape** — `register <name>` (operator/held-identity,
   mints for another) vs `register --self` (bootstrap/enrollment, mints for self);
   output `registered <name> as <ULID>` / `enrolled as <ULID>` + a creds path.
2. **presence column** in `clients list` (`online`/`offline`) and whether `list`
   shows offline clients by default (proposed: yes — that's the durable directory).
3. **`clients retire <id>`** as the decommission verb (vs `deregister`).
4. whether retire is operator-only for now (proposed: yes; managed-auth grants it
   to parents later, ADR-0009).
