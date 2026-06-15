---
id: TASK-75
title: >-
  Chat messages should be terse; enforce or nudge agents toward 144-char limit +
  artifacts for long content
status: To Do
assignee: []
created_date: '2026-06-13 03:26'
labels:
  - feature
  - protocol
  - lexicon
  - ergonomics
  - 'slug:feat-chat-message-length-limit'
  - P2
  - ready-for-human
dependencies: []
priority: medium
ordinal: 80000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Agents publish walls of text into chat topics. Lena wants a Twitter-style limit (~144 chars) on chat messages, with anything longer living in an artifact and the chat carrying only the headline. Could be enforced at the bus level (reject/truncate chat.message records over N chars) or a softer convention (SDK helper that auto-promotes long text to an artifact). Either way the goal is: chat = headlines, artifacts = detail.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A chat.message with text > 144 chars is either rejected at the bus level or the SDK emits a lint warning + auto-promotes to artifact
- [ ] #2 The lexicon/convention is documented: chat is for headlines; use a document artifact for anything longer
- [ ] #3 Existing agents (canopus, orion, vega, sirius) are briefed on the convention
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Design decision: bus-level enforcement (simpler, consistent) vs SDK helper (gentler, backward-compatible). Start with convention + SDK lint, escalate to bus enforcement if agents ignore it.
<!-- SECTION:PLAN:END -->
