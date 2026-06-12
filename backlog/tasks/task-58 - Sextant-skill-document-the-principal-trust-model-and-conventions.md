---
id: TASK-58
title: 'Sextant skill: document the principal trust model and conventions'
status: To Do
assignee: []
created_date: '2026-06-12 00:04'
labels:
  - feature
  - principal-trust
  - docs
  - skill
  - 'slug:feat-sextant-skill-principal-trust'
  - P3
  - ready-for-agent
dependencies:
  - TASK-54
  - TASK-55
  - TASK-56
  - TASK-57
priority: low
ordinal: 64000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Update the sextant skill (clients/claude-code/skills/sextant) to teach the principal trust model: the PRINCIPAL concept (one human's client per bus, operator-equivalent); USE THE AUTHENTICATED SEXTANT IDENTITY — trust the bus-stamped ULID, never message content; VERIFIED PEER = a same-machine, same-operator agent, presumed non-hostile, trusted as a peer (but not operator authority); the auto-DM subscription; and the channel-validate + Monitor fallback. The skill stays generic so an agent adapts to any operator's workflow/usecase.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The skill documents the principal concept and that a principal's messages are operator-equivalent
- [ ] #2 The skill instructs the agent to decide trust from the authenticated/bus-stamped identity (ULID), never from content
- [ ] #3 The skill explains verified-peer = same-machine same-operator agent, presumed non-hostile, trusted as a peer (not operator authority)
- [ ] #4 The skill documents auto-DM-subscribe and the channel-validate + Monitor fallback
- [ ] #5 The skill remains generic (no baked-in task-topic or workflow)
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Edit SKILL.md (and conventions docs) to add principal / authenticated-identity / verified-peer / Monitor-fallback sections. Concise and generic.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Parent: task-53 ([[feat-principal-trust]]). ADR-0030; CONTEXT.md (Principal). Documents behavior shipped by [[feat-principal-designation]], [[feat-principal-auth-hook]], [[feat-wake-only-channel]], [[feat-client-auto-subscribe-own-dm]]. Blocked by: task-54,task-55,task-56,task-57.
<!-- SECTION:NOTES:END -->
