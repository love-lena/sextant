# M2 AFK run — tracker

**Gate cleared:** ADR-0019 **accepted** (`f62c942`, Lena "Ship it" + edits applied).
This is the live tracker for the autonomous M2 build. Design = ADR-0018/0019 +
`protocol/`; milestones/DoD = `backlog/docs/doc-1 - Roadmap.md`.

## Governing rules (Lena, 2026-06-04)
- **Scope:** all of M2 to MVP. **Go-only** (TS SDK is Future/TASK-5).
- **Forks:** ask **codex** → best-judgment → **proposed ADR** + flag; never stall.
- **Merge:** **stack for review** — each PR based on the previous; I open, do NOT
  merge; Lena reviews + merges the stack on return.
- Rebuild PRs run **lint+test(Go)** only; gofumpt; `pkg/**` needs CHANGELOG;
  footer `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`;
  never `gh pr merge --admin`.

## Design deltas from ADR-0019 review (apply throughout the build)
- **ULID is the address for ALL three types** (clients, messages, artifacts);
  `display_name` is a non-keying attribute on clients + artifacts. `methods.json`
  artifact ops address by `id` (ULID), not name.
- **Call transport:** `sx.api.<clientULID>.<op>` request/reply over the existing
  JWT connection; **per-client ALLOW-list JWT** (flip from deny-only) makes
  `author` = the subject token (unforgeable). Push → `sx.deliver.<id>.*`. `msg.*`
  + KV become bus-internal. Reply-after-ack; bounded concurrent responders;
  bus-owned subscription cursors.
- **Backend interface:** semantic-contract as a Go interface; opaque synthesized
  cursors; watch needs a durable change stream (not bare keyspace events).
- **§5 is design philosophy**, not a hard M2 gate; idiomatic-NATS is fine; the
  hard guarantee is the unforgeable `author`.
- **Packaging:** Sextant impl under `internal/`; only `pkg/sextant` (SDK) exported.
  **Deep modules** (narrow interfaces, substantial impls).

## PR stack (stacked; bottom→top; status)
- [x] **PR1 `feat/m2-frame`** — `pkg/wire` Envelope→Frame (sender→author, ULID ids,
      kind message|artifact, artifact frame fields revision/createdAt/updatedAt).
      **PR #76 (open, base rebuild)** — build/vet/wire+sdk tests green.
- [x] **PR2 `feat/m2-backend-iface`** — `internal/backend` interface + NATS module
      + conformance suite. Redis-checked. **PR #77 (open, base m2-frame)** — green.
- [x] **PR3 `feat/m2-bus-serves`** — bus serves request/reply ops over `sx.api.*`;
      frame stamping; author from subject token; backend-served. **PR #78 (open)**.
      Push-stream (subscribe/watch) + artifact-ULID-addressing deferred to the cutover.
- [x] **PR4 `feat/m2-identity`** — client identity = bus-minted ULID + display_name
      (TASK-30 client half). **PR #79 (open)**. Artifact-ULID-addressing + artifact.list
      (the §3 artifact half) split to a later PR (entangled w/ SDK artifact methods).
- [x] **PR5 `feat/m2-sdk-client`** — THE CUTOVER, sliced (5a–5d-ii all landed;
      **the cutover is complete — nothing reaches the backend except through the
      bus, and the stamped author is unforgeable**). 5.5 below is a follow-on
      feature, not part of the cutover:
  - [x] 5a Publish→call + FetchMessages (message ops). **PR #80**.
  - [x] 5b ListClients→clients.list call (+ bus skips corrupt, key-authoritative id). **PR #81**.
  - [x] 5c data-plane cutover. **PR #83** (`feat/m2-cutover`, off #81). artifacts
        (create/update/get/delete → calls) + push-stream serving for
        message.subscribe & artifact.watch over sx.deliver.<id>.<subID> (relay
        registry + subscription.stop control op + shutdown teardown) + SDK
        Subscribe/WatchArtifact cutover. Artifacts coupled (bus stores FRAMES,
        direct stored raw) so all artifact ops moved as one unit. Codex-reviewed;
        lease/keepalive crash-teardown deferred to TASK-20 (same gap as the registry).
  - [x] 5d-i connect-handshake cutover. **PR #84** (`feat/m2-connect-cutover`, off
        #83). New internal ops clients.register (folds the epoch hard-gate — bus
        writes the record keyed by the subject token, returns {bus_epoch,
        connected_at}; registers only an epoch-compatible client) + clients.deregister
        (Close). Drain now delivers over sx.deliver.<id>.drain (bus keeps an in-memory
        `connected` set; "drain" subID reserved). SDK Client drops its JetStream
        handle; dead registryRecord/checkRecordKey removed. Deny-only perms KEPT →
        all green. TestDrainDelivers rewritten; new TestRegisterEpochGate.
  - [x] 5d-ii THE ALLOW-LIST FLIP — the security keystone. **PR #85**
        (`feat/m2-allowlist`, off #84). clientPermissions(clientID) deny→allow:
        `Pub.Allow:[sx.api.<id>.>]`, `Sub.Allow:[sx.deliver.<id>.>,
        _INBOX.<id>.>]`. `Resp` (allow_responses) NOT needed and OMITTED — the
        client is a requester, never a responder. With this NOTHING is direct →
        author unforgeable. Operator-side write seams added on `*Bus` (real methods,
        not on opConn): `SetEpoch`/`SeedClientRecord`/`DeleteClientRecord`/
        `InjectMessage` — the writes only the bus can do now clients have no backend
        access. Test rework: DELETED TestClientCanWriteConventionBuckets (premise now
        false) + added TestClientRegistersViaCall; TestStartBootstrapsBuckets +
        TestNoOperatorOnlyBucket → operator conn (`b.opConn`); client_test
        TestConnectRegisters/TestCloseLeavesRegistry → ListClients; epoch/empty/
        corrupt/skew/quarantine → the new seams. **bus tests now thread the minted
        ULID as the call subject** (the deny-only suite used arbitrary tokens; the
        allow-list rejects them — this was the non-obvious breakage). KEPT
        TestNoOperatorOnlyBucket + TestClientCannotPublishControl (stronger now).
        **Follow-up `681d620` (review fix):** independent Codex adversarial review
        found a [High] gap — shared `_INBOX.>` let any client eavesdrop on every
        other client's call replies. Fixed with a PER-CLIENT inbox: credential
        allows only `_INBOX.<id>.>`, SDK sets matching `nats.CustomInboxPrefix`
        (`wireapi.InboxPrefix`), + `TestClientCannotSubscribeForeignInbox`. Also
        hardened clientPermissions with a fail-loud guard for non-token ids.
  - [ ] 5.5 artifact-ULID-addressing + artifact.list (§3 artifact half; methods.json name→id).
- [x] **PR6 `feat/m2-cli`** — TASK-28: CLI (op-name parity) + conformance test. **PR #82**.
      Smoke-verified the M2 loop end-to-end (2 clients exchange msg + CAS artifact via bus).
      Recorded the loop as a charm-VHS demo (`docs/demos/m2-collaboration-loop.{tape,gif}`),
      committed onto #82 (`1651951`).
- [ ] **PR7 `feat/m2-mcp`** — TASK-22: MCP server + channel + skill (CC plugin).
- [ ] **PR8 `feat/m2-ergonomics`** — TASK-27: run/up --with-dir/per-client creds/
      --reclaim + getting-started + M2 DoD e2e.

Acceptance spine: the conformance test (PR6) + the M2 DoD e2e (PR8).
Parked: TASK-23 (request/reply), TASK-20 robust liveness (only --reclaim stopgap).

## Resumability
**THE CALL-TRANSPORT CUTOVER IS DONE; the IDENTITY HALF (ADR-0020) REMAINS and
ships with it.** Open PRs **#76–#85** (all green, stacked, unmerged). DAG: #81 ←
#82 (CLI); #81 ← #83 (data-plane) ← #84 (connect-handshake, 5d-i) ← **#85
(allow-list flip, 5d-ii)**. After #85 nothing reaches the backend except through
the bus and the stamped author is unforgeable — but per Review round 2 (below) the
stack is **M2 part 1 (call transport), not the finished milestone**: the
bus-issued-identity model (ADR-0020) is M2's identity half and must land + ship
with it. Do not merge #76–#85 alone.

Current goal (Stop hook, 2026-06-05): *cutover complete + all PRs self-reviewed +
change stories sent to Lena's Manta for review.* **ALL THREE DONE:**
- **(1) Cutover** ✓ — #85 + the per-client-inbox review fix; full stack green.
- **(2) Self-review** ✓ — a self-review comment on every PR #76–#85 (a reviewer's
  guide: what to scrutinize + residual flags), and an independent Codex adversarial
  review of the keystone (#85) that caught the eavesdrop gap, now fixed + posted.
- **(3) Change stories** ✓ — `backlog/m2-cutover-change-stories.md` rendered to an
  editorial PDF and uploaded to the Manta `/INBOX/m2-cutover-change-stories.pdf`
  (device pulls on next sync).

**Review round 1 — DONE (2026-06-05).** Lena annotated the change-stories doc on
the Manta: **"Overall LGTM"** + 2 discussion Qs + a steer (*pre-1.0: don't care
about intermediate working state, only that it works in 1.0*). Pull annotated
exports via `supernote cloud download /EXPORT/<name>.pdf` — the device's EXPORT is
a flattened PDF with ink correctly placed; `flatten.sh`'s `.pdf.mark` composite
misplaced the ink (maps Nth inked sheet → PDF page N), so prefer the EXPORT pdf.
Both Qs resolved:
- **Q1** (is accreting `clients.<ops>` ok?) → register/deregister stay as
  client-called, bus-validated control ops; reframed the "plumbing" doc comment.
  Commit `a74f0a6` on **#84**.
- **Q2** ("cleanest option even if more work" for the operator seams) → moved off
  the production `*Bus` into `pkg/bus/export_test.go` + `package bus_test` (drives
  the SDK via the external-test-package-imports-its-importer rule); **no build
  tag**. New `docs/conventions/test-features.md` captures the ladder (rung 3 =
  export_test.go; build tags reserved for rung 4 = instrumenting a built binary
  for out-of-process e2e, not needed yet). Commit `7c12f25` on **#85** (with #84
  merged forward). Lena: *"that's the last thing for my approval."*

**Review round 2 — ADR-0020 (2026-06-05).** A long design grilling produced
**ADR-0020 "Clients are bus-issued identities"** (`docs/adr/0020-...`, on rebuild).
Lena annotated it on the Manta: **approval throughout (checkmarks + signature)**,
with ONE correction that changes the plan. My ADR said "nothing blocks merging the
cutover as it stands"; Lena: *"It does — we need this fix to finish this milestone,
and it will ship together."* So:

**⚠ THE CUTOVER IS *NOT* DONE-AND-MERGEABLE ON ITS OWN. ADR-0020's identity model
is the identity HALF of M2 and ships together with #76–#85 as one milestone — DO
NOT merge the stack alone.** Hold the merge until the identity model is implemented
and folded in. (Sign-off: Lena's signature + checkmarks read as approval of the
decision; the formal `status: accepted` flip is still hers to make — not yet
flipped.)

**The model (ADR-0020), concretely, = the next inflight build work:** (a) **one
issuance path** `clients.register` accepting two auth modes — a held identity
(issuer mints for another) OR a bootstrap authorization with no pre-existing
identity (enrollment: locality at the weak end, a signed token at the strong end;
mint for self) — same mint-and-return either way; the bootstrap/enrollment
connection tier (local-trust / auth-callout) is the genuinely new mechanism;
(b) **`token` → `register`**: retire offline minting, keys stay in the bus, the
operator credential is provisioned at `sextant up`; (c) **durable identity store**
(persist issued identities, survive restart, recognize reconnect by key);
(d) **connection-derived presence** ($SYS / the embedded server's connection table;
no heartbeat, no ghost-reaping); (e) **`deregister` → `retire`** (decommission;
`Close` just goes offline); (f) `clients.list` = registered ⨝ presence; update the
ADR-0019 identity half + `protocol/nats-binding.md` handshake notes.

Open flags carried (not blockers): `clients.list` skip-quietly shift (#81),
crash-teardown → TASK-20 (#83), drain-as-signal vs os.Exit (ADR-0010).

Then the rest of M2: **PR5.5** artifact-ULID-addressing, **PR7** MCP (TASK-22),
**PR8** ergonomics + M2 DoD e2e (TASK-27). M2 ships when all of it is green
together.

**▶ START THE NEXT (IMPLEMENTATION) SESSION HERE:**
`backlog/m2-identity-impl-brief.md` — self-contained brief whose goal is making
**`tests/e2e/m2-acceptance.md`** (the executable DoD; CLI/output surface decided
2026-06-05) pass end-to-end. It maps the ADR-0020 work to the acceptance steps,
gives a green-sliced build order (stack on #85), the verify path, and the process
rules. The acceptance spec is the definition of done.

Remaining BUILD work (beyond the current review goal): **PR5.5**
artifact-ULID-addressing + artifact.list (§3 artifact half; methods.json name→id),
**PR7 MCP** (TASK-22: server + channel + skill), **PR8 ergonomics** (TASK-27:
run / up --with-dir / per-client creds / --reclaim + getting-started + M2 DoD e2e).
PR6 already proved the DoD loop via CLI (+ VHS demo on #82); PR8 documents it.

Keep each commit green; metadata→rebuild, shipping→PR+CHANGELOG. Each completed PR:
check the box, record the PR number, update the handoff buffer
(`~/dev/sextant/.remember/remember.md`).
