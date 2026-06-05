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
  - [x] 5a Publish→call + new FetchMessages (message ops). **PR #80 (open)**.
  - [ ] 5b ListClients + artifacts (create/update/get/delete) → calls.
  - [ ] 5c push-stream serving (subscribe/watch over sx.deliver) + SDK Subscribe/
        WatchArtifact cutover + per-client ALLOW-list flip (deny direct msg.*/KV;
        permit sx.api.<id>.> + sx.deliver.<id>.> + _INBOX.>; allow_responses).
  - [ ] 5.5 artifact-ULID-addressing + artifact.list (§3 artifact half; methods.json name→id).
- [ ] **PR6 `feat/m2-cli`** — TASK-28: CLI (op-name parity) + conformance test.
- [ ] **PR7 `feat/m2-mcp`** — TASK-22: MCP server + channel + skill (CC plugin).
- [ ] **PR8 `feat/m2-ergonomics`** — TASK-27: run/up --with-dir/per-client creds/
      --reclaim + getting-started + M2 DoD e2e.

Acceptance spine: the conformance test (PR6) + the M2 DoD e2e (PR8).
Parked: TASK-23 (request/reply), TASK-20 robust liveness (only --reclaim stopgap).

## Resumability
Current: **PR5b next** (open PRs #76–80). Branch the next slice off `feat/m2-sdk-client`.
Note: after the cutover slices, **PR6 CLI (TASK-28)** can build on the SDK as-is
(Publish/FetchMessages are calls; Subscribe/artifacts/ListClients still work direct).
Resume from this tracker; keep each commit compiling + green. NOTE: remaining stack may
grow beyond 8 PRs — identity / SDK-cutover / artifact-ULID are entangled in the SDK,
so split into smaller green PRs as needed. Each
completed PR: check the box, record the PR number, update the handoff buffer
(`~/dev/sextant/.remember/remember.md`).
