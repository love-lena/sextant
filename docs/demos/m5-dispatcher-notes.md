# M5.2 reference dispatcher — design notes (TASK-25)

_By canopus, 2026-06-12. Graduates the M5.1 spike ([spawn-spike-notes.md](spawn-spike-notes.md),
`cmd/spawn-poc`) into the reference dispatcher. Validated end-to-end on a
**throwaway bus** (fresh store + port, never the operator's live bus):
`docs/demos/m5-dispatcher-demo.sh` → **10/10**. Feeds M5.3 (TASK-23) and M5.4
(TASK-26)._

## What M5.2 adds

M5.1 proved a process, handed creds + a store, joins the bus and wakes on a DM.
M5.2 is the thing that **does the handing**: a long-running dispatcher that
subscribes to `spawn.request`, mints a named child identity, launches it, ack's
the new id, and supervises it — and a spawned child can itself drive the
dispatcher (recursion).

| AC | What it is | Status | Evidence |
|----|-----------|--------|----------|
| #1 | `spawn.request` / `spawn.ack` message kinds (job + parent lineage) | ✅ | `protocol/lexicons/spawn.{request,ack}.json` + `cmd/sextant-dispatch/records.go`; round-trip + reject + lexicon-parse in `records_test.go` |
| #2 | Dispatcher subscribes to `spawn.request`, launches a local subprocess, ack's the new id | ✅ | `cmd/sextant-dispatch`; demo: `boss` requests `alpha` → child joins, dispatcher publishes `spawn.ack` with id + `status:ok` |
| #3 | Spawned client joins under its **own named identity** and participates | ✅ | demo: `alpha` is a `kind=agent` named entry in `clients list` (not `claude-<hex>`); its `hello` frame's author is its own minted id |
| #4 | Recursion: a spawned client can itself publish `spawn.request` | ✅ | demo: `alpha`'s harness publishes a `spawn.request` for `beta`; the dispatcher stands up the grandchild; `beta`'s `spawn.ack` carries `parent == alpha` (bus-stamped lineage) |
| #5 | Supervisor / agent-runner is its OWN bus client; wakes the one-shot on inbound | ✅ | dispatcher launches `cmd/spawn-poc` per child; demo: `boss` DMs `alpha` → supervisor re-invokes the harness → `alpha` publishes `awake-ack` under its same id |
| #6 | **mint-on-behalf**: scoped per-agent creds via the bus (sole minter) | ✅ | locked-core change ([ADR-0033](../adr/0033-a-dispatcher-mints-its-own-workers.md)); `pkg/bus/mint_on_behalf_test.go` (a top-level client of any kind dispatches; a spawned worker is refused; the dispatcher keeps minting); demo: the dispatcher runs `--on-behalf` with no operator creds — every child is minted by its own authority |

## Architecture

```
spawn.request ─▶ [ dispatcher ]  (its own bus identity)
                    │  mint child (named, kind=agent)        ── AC#6 mint-on-behalf / issuer
                    │  launch harness  (SEXTANT_CREDS=child, $SX_PROMPT)   ── one-shot
                    │  launch supervisor (cmd/spawn-poc, its own client)   ── AC#5 wake loop
                    └▶ spawn.ack {id, nickname, job, parent, status}
```

- **The spawn lexicon (AC#1)** is ordinary message records on a normal `msg.*`
  subject — opaque to the bus, **no wire-protocol or epoch surface**. A
  `spawn.request` carries **data only** (a prompt + lineage labels), never a
  command: the dispatcher always runs its OWN configured `--harness`, so a request
  from any client can't inject code onto the dispatcher's host. The lineage parent
  is the **bus-stamped frame author**, never a self-declared field.
- **The dispatcher (`cmd/sextant-dispatch`, AC#2)** subscribes to `--subject`
  (default `msg.topic.spawn`) with `DeliverAll`, dedups by request frame id, and
  for each request mints → launches → ack's. It is harness-agnostic: the harness
  is a command template run via `sh -c` with `SEXTANT_CREDS` (the child's),
  `SEXTANT_STORE`, `$SX_PROMPT`, `$SX_CHILD_ID/NICK`, `$SX_JOB` in its environment.
  So the demo's stub harness (`sextant publish`) and M5.1's live `claude -p` /
  `codex exec` plug into the same seam.
- **The supervisor (AC#5)** is `cmd/spawn-poc`, launched per child — literally its
  own bus client (separate process + connection). The dispatcher passes the child's
  `SEXTANT_CREDS` in the supervisor's environment so the woken harness rejoins
  under the **child's** identity (spawn-poc's explicit `--creds` still drives the
  supervisor's own connection).
- **mint-on-behalf (AC#6)** is the lone serial locked-core change — see
  [ADR-0033](../adr/0033-a-dispatcher-mints-its-own-workers.md). The dispatcher
  picks its minting authority by flag: `--on-behalf` (its OWN client authority —
  any top-level client may, the demo's path) or `--issuer-creds` (an
  operator/enroll issuer, for a dispatcher run with operator authority). Both
  return the same `IssuedClient{ID, Creds}`. The fence is **inverted from an
  allowlist**: the bus stamps `SpawnedBy=caller` on every mint-on-behalf child, and
  authorizes `clients.register` from any client whose own record has no `SpawnedBy`
  — so a spawned worker can't dispatch, but it doesn't depend on the weakly-enforced
  `kind`.

## Recursion + lineage (AC#4)

A spawned child publishes a `spawn.request` like any other client; the dispatcher
(still subscribed) honours it. Minting authority is **not** handed down the tree —
a spawned worker is fenced out of dispatching (ADR-0033's inverted rule), so the
child never mints, it asks the dispatcher, and the (top-level) dispatcher mints.
That bounds the tree to the clients the operator actually brought on, with no
fork-bomb path through the workers. The `job` label is propagated request→request
so a whole tree shares one job id; the `parent` on each `spawn.ack` is the
bus-stamped author of the request that created it (the registry `SpawnedBy` instead
names the minting dispatcher — intent-lineage vs authority-lineage).

## Running it

```
docs/demos/m5-dispatcher-demo.sh          # build, throwaway bus, all 6 ACs
SX=/path/to/sextant docs/demos/m5-dispatcher-demo.sh   # reuse prebuilt binaries
```

**Token-free:** the spawned client is a stub harness (the `sextant` CLI), not a
model — M5.1 already proved the live `claude -p` / `codex exec` harness and
resume-wake, so this demo proves the *dispatcher mechanism* deterministically.

## Hand-offs

- **M5.3 (TASK-23, `sextant run` / request-reply)** builds on the same lexicon +
  dispatch loop: a request with a reply subject, a worker that answers once.
- **M5.4 (TASK-26, workflow coordinator)** composes dispatchers — a coordinator
  issues `spawn.request`s and tracks `spawn.ack`s by `job`.
- **Inherited from M5.1 (still open):** re-invoke dedup/coalescing of overlapping
  wakes; the exit-hook → `--watch` set (a spawned agent declaring topics to stay
  alive on); MCP-readiness on `--resume`; zombie-client cleanup for one-shot exits.
- **New, deferred (PoC scope):** spawn-rate limiting per dispatcher; persistence of
  handled-request ids across a dispatcher restart (dedup is in-memory today);
  registry-level lineage (parent is on the `spawn.ack` message, not the directory
  record).

## Safety

Every experiment runs on a throwaway bus (`sextant up --store <tmp> --port`), fresh
per run; the demo tears down the bus, the dispatcher(s), and all spawned children
on exit. No spawned identity touches the operator's live bus.
