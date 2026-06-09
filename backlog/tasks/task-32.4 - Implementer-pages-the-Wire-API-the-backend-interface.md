---
id: TASK-32.4
title: 'Implementer pages: the Wire API + the backend interface'
status: In Progress
assignee: []
created_date: '2026-06-08 22:42'
updated_date: '2026-06-09 00:06'
labels:
  - docs
  - 'slug:docs-mdbook-implementer-pages'
  - P3
  - ready-for-agent
dependencies:
  - TASK-32.1
parent_task_id: TASK-32
priority: medium
ordinal: 38000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The second-SDK / second-backend audience (the TS SDK TASK-5 and any new backend build against these). Two pages: (a) The Wire API — the backend-neutral call transport (how a call maps to a request), which requires factoring the transport description OUT of protocol/nats-binding.md into backend-neutral canon (NATS is internal per protocol/README); (b) The backend interface — render protocol/semantic-contract.md, which is already prose.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 protocol/ refactor: the backend-neutral call-transport (Wire API) is factored out of nats-binding.md into canon; NATS specifics stay in nats-binding.md (internal)
- [x] #2 The Wire API book page renders the backend-neutral transport (ADR-0019)
- [x] #3 The backend interface page renders protocol/semantic-contract.md (ADR-0018)
- [x] #4 No NATS-specific detail leaks into the client-facing book (protocol/README rule)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Serves the TS SDK (TASK-5) parallel module + any second backend. The nats-binding split is a real protocol/ canon change -> human sign-off. Depends on [[docs-mdbook-ia-render-pipeline]]. Refs ADR-0018, ADR-0019.

PROSE/AGENT SPLIT: agent owns the nats-binding→canon refactor (relocating the backend-neutral transport text out of nats-binding.md — a move, not net-new authoring) and rendering the backend-interface page from semantic-contract.md. The Wire API page is pre-stubbed with Lena's outline (branch docs-mdbook-scope); the relocated canon fills it, Lena polishes. Canon edits get human sign-off.

IMPLEMENTED in PR #97 (commit 05c01e6), CI green. Agent portion complete; remaining work is Lena's prose pages. Verified: docgen deterministic + CI drift-check, mdbook builds clean, quickstart compiles + runs against a live bus. NOTE: protocol/wire-api.md is new technical canon I authored (factored from nats-binding + methods.json); flagged for Lena since prose is normally hers.

RESTRUCTURED (commit 736760b): backend-interface page still rendered from semantic-contract.md (factual, done). The Wire API page is now a PROSE BLANK for Lena — my authored protocol/wire-api.md was removed and nats-binding.md restored; the backend-neutral-transport factoring is deferred to when Lena writes that page (noted in the stub + docs/book/AUTHORING.md).
<!-- SECTION:NOTES:END -->
