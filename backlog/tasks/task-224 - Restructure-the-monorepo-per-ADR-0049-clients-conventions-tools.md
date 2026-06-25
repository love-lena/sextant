---
id: TASK-224
title: 'Restructure the monorepo per ADR-0049: clients, conventions, tools'
status: In Progress
assignee: []
created_date: '2026-06-25 03:10'
updated_date: '2026-06-25 20:57'
labels:
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 213000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Problem Statement

The monorepo layout is language-shaped (clients/go, clients/ts) and hides what each directory is *for*. Several directories are two things at once — apps/workflow and apps/dispatch each bundle a record contract with the process that drives it; the browser dash SPA lives inside a Go app's internal/ tree; shared Go helpers sit under a vague apps/internal. A newcomer cannot tell the clients from the libraries from the build tools by reading the tree, and the co-equality story is muddied by where things live.

## Solution

Adopt the ADR-0049 structure: every unit is exactly one of a **client** (a process with a bus identity), a **convention** (records + verbs, no identity), or a **tool** (reads/emits, never connects). Clients become flat vertical peers under clients/, grouped by role not language. Conventions rise to a promoted, offered tier (conventions/<name>/{go,ts}). The SDKs, the Go host-helpers, and the tui component library become top-level libraries. Result: `ls clients/` lists the processes, `ls conventions/` lists the opinionated ways, and no directory is secretly two things. Target tree is in ADR-0049.

## User Stories

1. As a contributor new to the repo, I want `ls clients/` to list every running client as a peer, so that I can see what processes exist without learning a language-based layout.
2. As a contributor, I want `ls conventions/` to list the opinionated ways to use the primitives, so that I find goal/review/assistant/workflow/spawn in one place.
3. As a contributor, I want each directory to be exactly one of client/convention/tool, so that I never guess whether a dir holds a process, a contract, or a build tool.
4. As a maintainer, I want apps/workflow split into the workflow convention + the coordinator client, so that the reusable contract is reusable and the driver is just a client.
5. As a maintainer, I want apps/dispatch split into the spawn convention + the dispatcher client, so that the spawn-request contract lives beside the other conventions.
6. As a maintainer, I want docgen moved under the SDK as a tool, so that it stops masquerading as a client (it never connects to the bus).
7. As a maintainer, I want the browser dash SPA lifted out of clients/go/apps/internal/dashapi/web into its own web-dash client, so that a co-equal browser client is not buried inside a Go app's internals.
8. As a maintainer, I want pi-bus relocated from clients/ts into clients/ as a harness-plugin peer, so that harness plugins (pi-bus, claude-code) read as a role, not a language bucket.
9. As a maintainer, I want the CLI dir named sextant-cli (command stays `sextant`), so that the directory says what it is.
10. As a maintainer, I want the terminal cockpit named sextant-tui owning its tui components, so that the TUI library nests with its only real consumer instead of pretending to be top-level infrastructure.
11. As a maintainer, I want violet renamed to the assistant client (runtime identity stays "violet"), so that the implementation is named after the convention it implements.
12. As a maintainer, I want the ex-clientkit helpers (clictx, selfenroll, seqcursor, version) under shared/go as independently-named packages, so that their purpose is self-evident and they stay out of the SDK.
13. As a maintainer, I want dashapi/dashserve moved into sextant-dash, so that the web server's HTTP face lives with the server, not in a generic kit.
14. As a maintainer, I want importcheck rules rewritten to the new isolation lines, so that bus-clients, conventions->protocol-only, tui->theme-only, and SDK-helpers separation are mechanically enforced after the move.
15. As an operator, I want every binary to still build, launch, and connect after the move, so that the restructure changes nothing I can observe.
16. As an operator, I want make/brew to still produce the same runnable binaries, so that install and release are unaffected.
17. As a reviewer, I want the moves rename-preserving, so that a huge diff reviews as renames rather than delete+add.
18. As a maintainer, I want co-equality preserved (conformance vectors unchanged and green), so that the convention moves provably do not alter wire behaviour.
19. As a maintainer, I want the Go-review coverage gap decided explicitly (browser-only by design, or build the co-equal Go review convention), so that the gap is declared rather than discovered.

## Implementation Decisions

- Dirs created/moved per ADR-0049's target tree: sdk/{go,ts,docgen}; conventions/<name>/{go,ts}; shared/go/{clictx,selfenroll,seqcursor,version}; tui nested under clients/sextant-tui; flat clients/ with renamed/split peers; dashapi/dashserve into clients/sextant-dash.
- Splits: apps/workflow -> conventions/workflow + clients/coordinator; apps/dispatch -> conventions/spawn + clients/dispatcher; apps/docgen -> sdk/docgen (tool); apps/violet -> clients/assistant.
- One root go.mod spans the Go packages; all package import paths change module-wide in lockstep.
- importcheck rules rewritten to the new layout and kept as the structural gate.
- Lands as ONE orchestrated PR (intermediate states are painful; nothing intermediate merges), driven by a large model managing subagents.
- Subagent execution: to avoid concurrent subagents colliding on the same checkout, subagents own disjoint target areas and the cross-cutting import-path rewrite is a single serial pass owned by the orchestrator (or each subagent works in an isolated worktree and the orchestrator merges).
- Rename-preserving moves (git mv) so history and review survive.
- Command names unchanged (the CLI command stays `sextant`); only directory/module names change.
- No new bus operation, no protocol change, no behaviour change.

## Testing Decisions

- No new test seams. Reuse the highest existing ones: the conformance suite (cross-language wire contract; vectors are data, only import paths change), internal/importcheck (passing on new paths proves the isolation lines survived), and the full build/test/lint gate + tests/e2e (behaviour unchanged).
- A good test here is an existing behaviour test that still passes UNCHANGED. If a move forces a new or edited behaviour test, that is the signal the move was not behaviour-preserving — rework the move, do not adjust the test.
- Acceptance is the final branch green; intermediate red is expected (one PR).
- Prior art: the per-convention conformance_test.go; the imports_test.go / importcheck enforcement; tests/e2e.

## Out of Scope

- The TS SDK barrel / co-equal-seam fix (hiding wire/codec/frame exports) — a separate ADR + ticket.
- Any behaviour change, new convention, or new bus operation.
- Promoting shared/go helpers into the SDK (explicitly rejected — would widen the Go SDK past TS).
- A second/in-memory backend adapter (separate, speculative).
- Building a new Go review convention if review-write turns out browser-only (the decision to declare it is in-scope; the build, if chosen, is a follow-up).

## Further Notes

Decision recorded in ADR-0049 (this PR) with the explicit target tree; vocabulary in CONTEXT.md (harness plugin, tool, the client/convention/tool split). The green gate IS the review for a diff too large to read line-by-line — hence the weight on live-operability and rename-preserving moves.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Tree matches ADR-0049's target layout
- [ ] #2 importcheck rewritten to the new isolation lines and passing: bus imports only protocol; conventions import only protocol; tui imports only theme; shared/go never imported by the SDK; no client imports another except sextant-cli -> sextant-dash launcher
- [ ] #3 Conformance suite green with vectors unchanged
- [ ] #4 Full gate green: go build ./..., go test ./... -race, TS builds + vitest, make lint, tests/e2e
- [ ] #5 Live operability: sextant, sextant-dash, sextant-tui, dispatcher, coordinator, assistant, sextant-mcp each build, launch, and connect to a bus; build scripts + Homebrew formula updated so make/brew still produce them
- [ ] #6 Vocabulary honoured: coordinator/dispatcher/assistant clients; workflow/spawn conventions; docgen tool
- [ ] #7 Moves are rename-preserving (git mv) so the diff reviews as renames, not delete+add
- [ ] #8 No behaviour assertions added or changed; no new test seams
- [ ] #9 Review is co-equal (decided 2026-06-25): this restructure relocates conventions/review -> conventions/review/ts and reserves the conventions/review/go slot; the Go build + review conformance vectors are tracked separately as TASK-239, kept out of this PR so it stays purely behaviour-preserving
<!-- AC:END -->
