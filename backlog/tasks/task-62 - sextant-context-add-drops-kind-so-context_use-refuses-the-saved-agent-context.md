---
id: TASK-62
title: >-
  sextant context add drops --kind, so context_use refuses the saved agent
  context
status: To Do
assignee: []
created_date: '2026-06-12 17:46'
labels:
  - bug
  - cli
  - identity
  - clictx
  - ergonomics
  - 'slug:bug-context-add-missing-kind'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 68000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
sextant context add <name> --creds F saves a context record but, unless --kind agent is passed explicitly, leaves kind empty in the record (internal/clictx Context.Kind). The sextant-mcp context_use tool then refuses to attach: refusing to switch (kind empty): context_use attaches only to agent identities (cmd/sextant-mcp/conn.go use). So an operator who mints an agent identity (clients register <name> --kind agent) and saves it as a context cannot have a worker assume it -- the kind the bus already knows is silently dropped on the client side. Hit on 2026-06-12 saving a freshly-minted agent context; the workaround was a second context add --force --kind agent. The auto-mint path (selfenroll.EnrollAgent) stamps kind correctly, so only hand-added contexts are affected.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 After clients register <name> --kind agent then context add <name> --creds <f>, context_use <name> attaches without re-specifying --kind
- [ ] #2 context add infers kind from the bus directory by ULID when --kind is omitted, OR context_use falls back to the directory when the local record has no kind
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Prefer: context add looks the client up by --id/ULID in the bus directory and records its Kind when --kind is omitted. Alternative: context_use (conn.go use) falls back to the bus directory Kind when the local record Kind is empty instead of treating empty as non-agent. Either removes the silent drop.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: 2026-06-12 (canopus session) while renaming a worker via mint + context add + context_use. Related: [[feat-client-rename]], TASK-31 (saved client contexts, ADR-0021). Files: cmd/sextant/context.go contextAdd, cmd/sextant-mcp/conn.go use, internal/clictx/clictx.go.
<!-- SECTION:NOTES:END -->
