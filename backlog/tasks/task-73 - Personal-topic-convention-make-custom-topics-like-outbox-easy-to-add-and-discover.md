---
id: TASK-73
title: >-
  Personal topic convention: make custom topics (like outbox) easy to add and
  discover
status: To Do
assignee: []
created_date: '2026-06-13 02:33'
labels:
  - feature
  - dash
  - protocol
  - ergonomics
  - 'slug:feat-personal-topic-convention'
  - P2
  - ready-for-human
dependencies: []
priority: medium
ordinal: 78000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena wants outbox as a real dash feature, but more importantly wants the pattern to be something any user could add for themselves. Today a topic is trivial to create (just publish), but the hard parts are: (1) discovery — how does the dash know to show it, and (2) agent wiring — how does an agent know to watch it. The vision: adding a personal topic should be as easy as editing a config artifact, with the dash and agents both reading that to auto-wire. Question to answer: what's the minimal convention that makes this self-serve?
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A user can declare personal/custom topics they own (e.g. outbox, scratch) in a discoverable way without code changes
- [ ] #2 The dash picks up declared topics and surfaces them without a deploy
- [ ] #3 An agent can subscribe to a user's declared topics without hardcoded subject names
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Candidate: a 'workspace' artifact that lists personal topics + panels; dash and agents read it on connect. Needs design decision on the convention shape.
<!-- SECTION:PLAN:END -->
