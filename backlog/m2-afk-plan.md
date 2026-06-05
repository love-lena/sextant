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
- [~] **PR5 `feat/m2-sdk-client`** — THE CUTOVER, sliced:
  - [x] 5a Publish→call + FetchMessages (message ops). **PR #80**.
  - [x] 5b ListClients→clients.list call (+ bus skips corrupt, key-authoritative id). **PR #81**.
  - [x] 5c data-plane cutover. **PR #83** (`feat/m2-cutover`, off #81). artifacts
        (create/update/get/delete → calls) + push-stream serving for
        message.subscribe & artifact.watch over sx.deliver.<id>.<subID> (relay
        registry + subscription.stop control op + shutdown teardown) + SDK
        Subscribe/WatchArtifact cutover. Artifacts coupled (bus stores FRAMES,
        direct stored raw) so all artifact ops moved as one unit. Codex-reviewed;
        lease/keepalive crash-teardown deferred to TASK-20 (same gap as the registry).
  - [ ] 5d auth allow-list flip + connect-handshake cutover (NEXT — the security
        keystone; stack off `feat/m2-cutover`). Flip clientPermissions() deny→allow
        (pub only sx.api.<id>.>; sub only sx.deliver.<id>.> + _INBOX.>;
        allow_responses; NO direct msg.*/KV) → author unforgeable. BUT the flip
        breaks the SDK connect handshake (epoch read / register / drain all use
        direct access), so those move to calls FIRST (clients.register/deregister,
        an epoch/hello op, drain over sx.deliver), THEN flip. Rework bus_test.go
        direct-KV tests + inspectJS raw-KV helpers + skew/quarantine tests to seed
        via the operator/bus conn (Codex: treat auth as the LAST step). Likely
        splits into 5d-i connect-cutover + 5d-ii flip-and-test-rework.
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
Current: open PRs #76–83 (all green, stacked, unmerged). DAG: #81 ← #82 (CLI) and
#81 ← #83 (data-plane cutover) ← PR5d (next). Remaining: **PR5d auth allow-list
flip + connect-handshake cutover** (the security keystone — see 5d above; the
hardest remaining slice, the only thing between "data plane through the bus" and
"author unforgeable"), **PR5.5** artifact-ULID-addressing, **PR7 MCP** (TASK-22),
**PR8 ergonomics + getting-started + M2 DoD walkthrough** (TASK-27). PR6 already
proved the DoD loop works via CLI (+ recorded VHS demo on #82); PR8 documents it +
adds `sextant run`/`up --with-dir`/`--reclaim`. Resume from this tracker; keep each
commit green. PR5d needs care (it breaks the connect handshake + several tests) —
do with fresh context; consider splitting 5d-i/5d-ii. NOTE: remaining stack may
grow beyond 8 PRs — identity / SDK-cutover / artifact-ULID are entangled in the SDK,
so split into smaller green PRs as needed. Each
completed PR: check the box, record the PR number, update the handoff buffer
(`~/dev/sextant/.remember/remember.md`).
