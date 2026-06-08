---
id: TASK-32.2
title: Protocol reference pages + the connection/auth/creds page
status: To Do
assignee: []
created_date: '2026-06-08 22:42'
updated_date: '2026-06-08 22:50'
labels:
  - docs
  - 'slug:docs-mdbook-protocol-reference'
  - P3
  - ready-for-agent
dependencies:
  - TASK-32.1
parent_task_id: TASK-32
priority: medium
ordinal: 36000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Render the protocol contract from canon (via 32.1's pipeline) plus the one new canon page. Generated pages: Operations (methods.json), Records & lexicons (lexicons/*.json), The frame (frame.json + ADR-0006), Clients registry & presence (client.json + ADR-0020), Epoch & versioning. Plus a NEW language-neutral canon page protocol/connection.md synthesizing connect/auth/creds (ADR-0012 + ADR-0020), since that is scattered across ADRs + SDK comments today and the TS SDK needs it too. Satisfies TASK-32 AC#1 and AC#2 in full.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Operations page generated from methods.json: every operation with its delivery, input/output, and semantics
- [ ] #2 Records & lexicons page generated from lexicons/*.json (chat.message, document, client, frame)
- [ ] #3 The frame page (frame.json + ADR-0006): record=user space / frame=bus space, the bus-stamped fields, the message|artifact kind discriminator
- [ ] #4 Clients registry & presence page (client.json + ADR-0020): durable directory, listed = issued-and-not-retired, presence derived at read time
- [ ] #5 Epoch & versioning rendered as a short reference page (generated slot); the Protocol Overview and Connection/auth pages are Lena's pre-stubbed prose — NOT authored in this ticket
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Mostly AFK generation; connection.md is new canon prose (technical, agent-draftable) -> human sign-off (canon <=> signed-off). Depends on [[docs-mdbook-ia-render-pipeline]]. Closes TASK-32 AC#1 + AC#2.

PROSE/AGENT SPLIT: this ticket fills only the generated draft slots (Operations, Records & lexicons, The frame, Clients registry & presence, Epoch) from protocol/ canon via 32.1's pipeline. The Overview + Connection pages are Lena's prose stubs (branch docs-mdbook-scope). OPEN (agent+Lena): whether Lena's Connection prose also becomes language-neutral canon protocol/connection.md for the TS SDK — decide when 32.2 lands. Refs ADR-0006 (frame), ADR-0020 (registry), ADR-0012 (auth).
<!-- SECTION:NOTES:END -->
