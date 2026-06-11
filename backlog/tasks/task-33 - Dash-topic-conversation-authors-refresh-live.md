---
id: TASK-33
title: 'Dash: topic-conversation authors refresh live'
status: To Do
assignee: []
created_date: '2026-06-09 19:05'
labels:
  - ready-for-agent
dependencies: []
ordinal: 39000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
R4 review minor (deferred): internal/dash resolves the id→author map once at launch, so a client enrolled after the dash starts renders as a raw short id in topic conversations until restart. DMs are immune — the clients browser rebuilds its map from live directory snapshots per open. Fix shape: refresh the shared authors map from the clients browser's periodic snapshot (or re-resolve on unknown id). Found in the feat/dash R4 review (ADR-0024 redesign), 2026-06-09.
<!-- SECTION:DESCRIPTION:END -->
