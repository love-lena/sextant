---
id: TASK-94
title: 'attest: also stamp peer DM topics (not just the principal DM)'
status: To Do
assignee: []
created_date: '2026-06-14 22:21'
labels:
  - feature
  - trust
  - mcp
  - 'slug:feat-attest-scan-peer-dms'
  - P3
  - ready-for-human
dependencies: []
priority: low
ordinal: 96000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The attest hook now scans the inbox + the principal DM it can compute deterministically (TASK-90). A DM with a non-principal PEER is not stamped — the hook has no session state listing which DM topics the agent joined, so the agent classifies peer DMs by the frame's bus-stamped sender_id itself (correct, but no pre-stamped convenience block). Options: have the MCP server record the agent's joined DM topics to a per-session file the hook reads, OR scan the deterministic DM wildcards (msg.topic.dm.*.<self> and msg.topic.dm.<self>.*). Then peer DMs get the same trusted block as the principal DM.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A peer DM (msg.topic.dm.<sorted self,peer>) is stamped VERIFIED PEER by the hook, delivered in the trusted block
- [ ] #2 Mechanism decided + documented (server-records-joined-topics vs wildcard scan); per-subject cursors preserved; fail-soft preserved
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Follow-up to [[feat-plugin-dm-default-over-inbox]] (TASK-90). ready-for-human: mechanism is a design choice (state file vs wildcard). Not a correctness gap — sender_id classification is the ground truth.
<!-- SECTION:NOTES:END -->
