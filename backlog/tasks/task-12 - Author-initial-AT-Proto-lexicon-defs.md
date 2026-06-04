---
id: TASK-12
title: >-
  Author the protocol source-of-truth: lexicons + methods + semantic contract +
  NATS binding
status: To Do
assignee: []
created_date: '2026-06-03 01:12'
updated_date: '2026-06-04 21:38'
labels: []
milestone: 'M2: MVP'
dependencies: []
references:
  - docs/adr/0006-wire-atom.md
priority: high
ordinal: 12000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The language-neutral source of truth that the SDK, CLI, MCP, and any BYO client conform to (ADR-0017), living in protocol/. Four artifacts: (1) lexicons/ - AT-Proto lexicon shape (minimal subset), the wire envelope + the M2 record shapes; NSID deferred per ADR-0017 (interim ids are the name minus the reverse-DNS authority, e.g. chat.message; records carry $type from day one). (2) methods.json - the verb index, transport-neutral, no NATS ops (ADR-0013 rule 1): per verb {input lexicon, output lexicon, delivery: one-shot|pull-batch|push-stream}. (3) semantic-contract.md - one page (ADR-0013 rule 2): durability, ordering, CAS, client-controlled replay, sender=authenticated identity, messages-enveloped vs artifacts-bare. (4) nats-binding.md - prose: how NATS realizes each verb. pkg/wire + pkg/sx are its Go expression. Full design: .work/rfcs/rfc-m2-verb-surface.md.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 protocol/lexicons/ defines the wire envelope + chat.message + artifact + client registry shapes in AT-Proto lexicon format (subset), with bare $type ids (NSID authority deferred, ADR-0017)
- [ ] #2 protocol/methods.json lists all 9 verbs, transport-neutral (no backend ops), each with input/output lexicon + delivery mode
- [ ] #3 protocol/semantic-contract.md states the behaviour any backend must honour on one page (ADR-0013 rule 2)
- [ ] #4 protocol/nats-binding.md documents the NATS realization well enough to write a non-Go NATS client
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
M2 subset: only the record shapes manual-comms needs — a chat message kind + the artifact record shape. spawn.request and workflow.event/envelope defer to M4.

Scope expanded from lexicons-only to the full protocol source-of-truth (ADR-0017): methods.json + semantic-contract.md + nats-binding.md fold in here, not a separate task. Directory: protocol/. M2 record shapes only - envelope, chat.message, artifact, client; spawn/workflow shapes stay deferred (M5).
<!-- SECTION:NOTES:END -->
