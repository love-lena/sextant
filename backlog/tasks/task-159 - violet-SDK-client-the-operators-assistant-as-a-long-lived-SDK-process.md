---
id: TASK-159
title: violet SDK client — the operator's assistant as a long-lived SDK process
status: To Do
assignee: []
created_date: '2026-06-17 21:02'
labels:
  - feature
  - violet
  - sdk
  - milestone
  - ready-for-agent
  - P1
  - 'slug:feat-violet-sdk-client'
dependencies: []
priority: high
ordinal: 149000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Build violet as a proper long-lived SDK client (NOT bash) per the handoff spec docs/demos/violet-sdk-handoff.md (branch feat-violet-warm-session). The bash spike proved the warm pseudo-agent design + surfaced every production issue; this is the real build. ONE bus identity + the assistant designation (ADR-0039); three internal model roles: haiku GATE (triage scoped+pre-filtered bus events) -> wakes sonnet HOME-MANAGER (deep context refresh + Home curation) only on significant events -> haiku CONVERSATIONAL (answers DMs from warm context). Default language Go (fits the repo's Go SDK); Python acceptable if preferred. Reuse the role prompt (docs/demos/violet-runtime.md) + the violet-curation skill (the tunable judgement) verbatim; the SDK replaces only the bash wrapper.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Operator DM answers in a few seconds EVEN under live-bus load (real concurrency: a priority DM consumer; gate on a bounded queue; deep refresh separate)
- [ ] #2 Answers <=250 chars, plain text, [[wikilinks]] only; accuracy strictly from current warm context ('I'll check' rather than guess)
- [ ] #3 Output-capture (the runtime publishes the reply; the model never calls publish) for both the answer + the context snapshot
- [ ] #4 Scoped subscription (operator DM, msg.topic.goals, msg.topic.artifact.>, msg.topic.crew — NOT msg.topic.> firehose) + cheap keyword pre-filter before the gate LLM + per-frame cursor + ignore-own-events
- [ ] #5 signal-not-manage: one bus client, answers + curates the home projection, never acts on the operator's behalf
- [ ] #6 Runs under violet's OWN scoped creds, never the principal's ambient creds (TASK-158)
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Per docs/demos/violet-sdk-handoff.md: SDK client w/ 3 concurrent roles sharing in-memory warm context; the 5 live-bus fixes from day one; avoid the 4 spike bugs (json-array output, no-file-write role, stub-masking, single-loop gate starvation). Deliver via a workflow (fresh workers), not the busy crew.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Handoff: docs/demos/violet-sdk-handoff.md. Reuse: violet-runtime.md + violet-curation skill. Executable reference: branch feat-violet-warm-session (d88cfd3->5b8f007->50724fc). Milestone: [[goal.violet]]. Relates: [[violet-architecture]], TASK-158 (creds). Build via WORKFLOW per Lena (fresh workers).
<!-- SECTION:NOTES:END -->
