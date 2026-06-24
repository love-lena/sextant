---
id: TASK-198
title: Dash redesign · EPIC A — Bus inspector (JetStream & KV explorer)
status: To Do
assignee: []
created_date: '2026-06-24 01:08'
labels:
  - dash-redesign
  - epic
  - lane-bus
dependencies: []
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: high
ordinal: 188000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The raw substrate explorer — NATS JetStream + Key-Value. New surface (no web/CLI/TUI/MCP equivalent exists today). Early/parallel: ships safely alongside the rest once the shell lands; high dev value. Children: TASK-195 (JetStream), TASK-196 (KV).

Carries AC section 19.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Bus nav row opens the inspector; both child slices merged
- [ ] #2 S19.1 mode toggle JetStream <-> Key-Value; left rail lists streams/buckets with name, subjects/keys, count, storage, live dot
<!-- AC:END -->
