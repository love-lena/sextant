# M2 identity model — implementation brief (start here)

**Goal of the session:** implement the bus-issued-identity model (ADR-0020) and
fold it into the M2 stack so **`tests/e2e/m2-acceptance.md` passes end-to-end**.
That acceptance spec is the definition of done. M2 ships as **one milestone** —
the #76–#85 cutover stack **plus** this identity half — so **do not merge the
cutover stack on its own**.

## The map (where everything is)

- **Repo:** `~/dev/sextant-rebuild`, branch `rebuild`. (The OLD build is
  `~/dev/sextant` — do not touch it; the shell cwd keeps resetting there, so use
  absolute paths / `git -C /Users/lena/dev/sextant-rebuild`.) `AGENTS.md` is
  canonical; `CLAUDE.md` symlinks to it.
- **Design (signed-in-spirit):** `docs/adr/0020-clients-are-bus-issued-identities.md`
  (`status: proposed`; Lena reviewed on the Manta — approval throughout +
  signature; the formal `status: accepted` flip is hers, not yet done — do NOT
  flip it). Refines ADR-0008/0012/0019.
- **Target / DoD:** `tests/e2e/m2-acceptance.md` — the collaboration loop as an
  executable spec, with the **decided** CLI/output surface (see its "Decided"
  section). Make this pass.
- **Status tracker:** `backlog/m2-afk-plan.md` (Resumability + "Review round 2").
- **The stack:** open PRs **#76–#85** (M2 part 1 = call transport; green, stacked,
  UNMERGED). DAG: `#81 ← #82 (CLI)`; `#81 ← #83 ← #84 ← #85`. The identity work
  stacks on **#85 (`feat/m2-allowlist`)**, the tip with everything merged forward.
- **Conventions:** `docs/conventions/test-features.md` (test seams),
  `docs/conventions/` generally, `AGENTS.md`.

## What to build (ADR-0020), mapped to the acceptance steps

1. **Issuance — one `clients.register` path, two auth modes** (acceptance step 1):
   - **held-identity mode** — an authenticated issuer (operator) mints for another:
     `sextant clients register <name>`.
   - **bootstrap/enrollment mode** — an identity-less local process mints for
     itself: `sextant clients register --self` (**explicit**, decided — not folded
     into connect). Authorized by **locality** for the MVP.
   - In both, the bus mints a new identity + returns its creds. Same op; auth mode
     is the gate. **register need not be an authenticated Wire-API call** — it
     needs *authorization* (held identity OR bootstrap trust), not authentication.
   - ⚠ **The genuinely new mechanism is the enrollment connection tier**: how an
     identity-less caller reaches the bus at all (it has no per-client credential
     and can't address `sx.api.<id>.>`). Design this — an anonymous/local-trusted
     connection tier, or NATS auth-callout at connect. This is the hard part and
     the one thing the #85 allow-list can't cover.
2. **`token` → `register`** (acceptance step 1; retire `sextant token`): no offline
   minting — the signing keys stay inside the bus (key custody). Provision the
   **operator credential at `sextant up`** so the human at the terminal can call
   `register` in held-identity mode. The CLI never reads the signing keys.
3. **Durable identity store** (steps 1, 5): the registry becomes a persisted store
   of issued identities — survives a bus restart, recognizes a reconnecting client
   by its authenticated key. (Today the registry is presence-only.)
4. **Connection-derived presence** (steps 4, 5): the bus computes online/offline
   from connection lifecycle — NATS `$SYS.ACCOUNT.*.CONNECT`/`DISCONNECT` events
   and/or the embedded server's own connection table — **not** from a register/
   deregister call. Replace the in-memory connected-set-from-register (it exists
   today for drain targeting; re-source it from connection events). This also
   **dissolves the stale-ghost / TASK-20 reaping problem** — a disconnected client
   is legitimately `offline`, not a ghost.
5. **`deregister` → `retire`** (step 6): `sextant clients retire <id>` decommissions
   an identity for good (operator-only for now). A clean `Close` goes **offline**,
   it does NOT retire. (So Close stops calling deregister.)
6. **`clients.list` = registered ⨝ presence** (step 4): show **offline clients by
   default**; add the `online`/`offline` presence column.
7. **Docs:** update the identity half of ADR-0019 and the connect-handshake notes
   in `protocol/nats-binding.md`; reconcile `protocol/methods.json` (per ADR-0020:
   `register` is the bootstrap exception with its own auth; `retire` and
   `clients.list` are ordinary authed ops; presence is derived, not an op).

## Decided CLI surface (Lena, 2026-06-05) — see `m2-acceptance.md` "Decided"

1. `clients register <name>` (operator mints for another) **and** `register --self`
   (explicit enrollment, mints for self). Output: `registered <name> as <ULID>` /
   `enrolled as <ULID>` + a creds path.
2. `clients list` has a `online`/`offline` presence column and shows offline
   clients by default.
3. `clients retire <id>` is the decommission verb.
4. `retire` is operator-only for now.

## Build order (suggested; keep commits green, stack on #85)

Branch off `feat/m2-allowlist` (#85). Suggested green slices (each its own PR,
stacked; M2 ships when all green together):
1. **Presence + durable store** — connection-derived presence ($SYS / conn table),
   durable identity records, `clients.list` presence column + show-offline. (Lets
   you green acceptance step 4 against issued identities.)
2. **Issuance + enrollment** — `register` two modes, the enrollment connection tier
   (the new mechanism), `token`→`register`, operator cred at `sextant up`. (Steps
   1, 2.)
3. **Retire + Close-goes-offline** — `clients retire`, `Close` stops deregistering.
   (Steps 5, 6.)
4. **The runnable e2e harness** — build it per `m2-acceptance.md` "How this becomes
   the runnable test": prove the plumbing against the *existing* loop first (steps
   2–4 work today with `token`), then turn the ADR-0020 steps green. Wire it into
   CI as the DoD e2e once it passes.

Pre-1.0 steer (Lena): don't over-invest in keeping every intermediate state
back-compatible — optimize for the end state (m2-acceptance green). Each commit
should still build+test green, but you needn't preserve the old token/register
behavior through the transition.

## How to verify (the DoD)

Make `tests/e2e/m2-acceptance.md` pass end-to-end: build the harness, run the
seven-step loop, normalize (ULIDs/timestamps/URL/paths), and match the expected
transcript + per-step asserts — especially the **unforgeable author** (step 2: the
frame bob receives has `author == alice's bus-minted id`, not a value alice chose)
and the **durable-identity-across-reconnect** (step 5: same id, presence flips).
When that's green *and* the rest of M2 is in (PR5.5 artifact-ULID, PR7 MCP, PR8
ergonomics), M2 is done and the whole thing merges together.

## Process rules (preserve)

- **gofumpt** (not gofmt) before pushing Go; `go build ./...`, `go vet ./...`,
  `go test ./...` green. Rebuild CI = `lint + test (Go)` only.
- Commit footer (exact): `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- **Metadata** (backlog/docs/adr/conventions/tests-spec) → commit straight to
  `rebuild`. **Shipping** (`pkg/**`, `cmd/**`) → via PR with a `CHANGELOG.md`
  `[Unreleased]` entry. **Stack for review**: open PRs, do NOT merge; never
  `gh pr merge --admin`.
- Use the **`EnterWorktree`** tool for isolated work.
- NATS is internal in client-facing protocol docs (only `protocol/nats-binding.md`
  names it); ADRs/binding/internal code may name it.
- **ADR sign-off is Lena's act** — do not flip `status: accepted` / add
  `signed_off_by` on ADR-0020.
- Env: macOS, no `timeout`/`pdftoppm` (use `mutool`); zsh does not word-split
  scalars (`set -- ${=var}` / list paths explicitly); shell cwd resets to the old
  build — absolute paths / `git -C`.

## Non-obvious learnings from the design sessions (don't re-derive)

- The **unforgeable author** comes from the per-client allow-list (#85): a client
  may publish only under `sx.api.<own-id>.>`, plus a **per-client inbox**
  `_INBOX.<id>.>` (NOT shared `_INBOX.>` — that let clients eavesdrop on each
  other's replies; fixed in #85). Don't regress these.
- **register is authorized, not authenticated** — the Wire API is authenticated +
  identity-scoped for the *unforgeable author of records*; identity *creation* is
  governed by *key custody* (keys live in the bus), so the enroll mode can be
  authorized by bootstrap trust without weakening anything.
- **presence = the connection**, computed by the bus; this is why there's no
  heartbeat and no ghost-reaping (supersedes the old liveness-heartbeat approach).
- Test seams that need another package's internals: `export_test.go` + an external
  test package (no build tag) — see `docs/conventions/test-features.md`.

## Pointers

- Design: `docs/adr/0020-...`, plus 0008/0012/0017/0019.
- Target: `tests/e2e/m2-acceptance.md`.
- Tracker: `backlog/m2-afk-plan.md`. Cutover narrative: `backlog/m2-cutover-change-stories.md`.
- Demo: `docs/demos/m2-collaboration-loop.{tape,gif}` (single-pane; the multi-pane
  version is a follow-up, scenario = m2-acceptance.md).
