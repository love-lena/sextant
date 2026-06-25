---
id: TASK-198
title: Dash redesign · EPIC A — Bus inspector (JetStream & KV explorer)
status: Done
assignee: []
created_date: '2026-06-24 01:08'
updated_date: '2026-06-25 02:31'
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

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in v0.8.0 (dash redesign; tag 275522a, 2026-06-24) — built across 5 parallel lanes, integrated on dash-redesign-demo, persona-swept, design-fidelity audited 0/0/0, reviewed live, released + verified on the managed dash (:8765).
<!-- SECTION:FINAL_SUMMARY:END -->
