---
id: TASK-196
title: 'Dash redesign: Bus inspector — Key-Value explorer'
status: To Do
assignee: []
created_date: '2026-06-24 00:33'
labels:
  - ready-for-agent
dependencies:
  - TASK-195
priority: low
ordinal: 186000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The Bus nav page, KV half (a mode toggle with JetStream). Lists buckets; bucket detail shows stats, a filterable key list with op + revision, the selected key's current value (JSON/Raw/Hex), and its full revision history (newest first) with tombstones for deleted keys. Browser-direct over the bus KV API (ADR-0044), no Go relay.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A mode toggle switches JetStream <-> Key-Value.
- [ ] #2 Buckets listed with name, keys, values count, storage.
- [ ] #3 Bucket detail: stats (values, history depth, ttl, bytes, max value) + a filterable key list with op + revision.
- [ ] #4 Selected key shows current value (JSON/Raw/Hex) + full revision history newest-first; deleted keys show a tombstone with history preserved.
<!-- AC:END -->
