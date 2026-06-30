---
id: TASK-255
title: 'Work-engine: strengthen single-writer proof to envelope-revision authorship'
status: To Do
assignee: []
created_date: '2026-06-30 00:46'
updated_date: '2026-06-30 00:47'
labels:
  - feature
  - workengine
  - work-engine
  - test
  - P3
  - 'slug:feat-single-writer-envelope-authorship-proof'
dependencies: []
ordinal: 241000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TASK-242's agent-mode single-writer AC (#3) is proven via the BUS-STAMPED AUTHOR of the agent's run.decision messages, not by reading the run envelope's revision authorship — because the SDK exposes no artifact-author field on the read path. The invariant holds by construction (one checkpoint() call-site on the shell's client; the reviewer agent has no envelope-write path), but the PROOF is a proxy. Strengthen it once an SDK/bus surface for artifact (envelope) author exists.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The agent-mode single-writer invariant is proven by reading the run envelope's revision AUTHOR (== the shell/coordinator client, never the reviewer agent) on every checkpoint, not by a decision-message-author proxy. Proof: a test that reads envelope-revision authorship after an agent-mode run and asserts the sole author is the shell. Depends on: an SDK/bus read surface for artifact author. Fake-pass guard: falsely passes if it still asserts message author rather than envelope author.
<!-- AC:END -->
