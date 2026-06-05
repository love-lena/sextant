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
- [ ] **PR1 `feat/m2-frame`** — `pkg/wire` Envelope→Frame (sender→author, ULID ids,
      kind message|artifact, artifact frame fields revision/createdAt/updatedAt).
      No ADR (frame.json specs it). *(in progress)*
- [ ] **PR2 `feat/m2-backend-iface`** — `internal/backend` interface + NATS module
      + conformance suite. Redis-checked.
- [ ] **PR3 `feat/m2-bus-serves`** — bus serves the 9 ops as calls; frame stamping;
      per-client allow-list JWT + author enforcement; `sx.api.*`/`sx.deliver.*`;
      `msg.*`+KV bus-internal.
- [ ] **PR4 `feat/m2-identity`** — TASK-30: ULID addressing uniform + display_name
      attribute; registry keyed by ULID; methods.json artifact name→id.
- [ ] **PR5 `feat/m2-sdk-client`** — Go SDK as bus client + FetchMessages.
- [ ] **PR6 `feat/m2-cli`** — TASK-28: CLI (op-name parity) + conformance test.
- [ ] **PR7 `feat/m2-mcp`** — TASK-22: MCP server + channel + skill (CC plugin).
- [ ] **PR8 `feat/m2-ergonomics`** — TASK-27: run/up --with-dir/per-client creds/
      --reclaim + getting-started + M2 DoD e2e.

Acceptance spine: the conformance test (PR6) + the M2 DoD e2e (PR8).
Parked: TASK-23 (request/reply), TASK-20 robust liveness (only --reclaim stopgap).

## Resumability
Current: **PR1 in progress** (gate just cleared). Each completed PR: check the box,
record the PR number, update the handoff buffer (`~/dev/sextant/.remember/remember.md`).
