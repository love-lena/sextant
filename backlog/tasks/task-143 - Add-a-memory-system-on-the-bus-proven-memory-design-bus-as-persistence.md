---
id: TASK-143
title: 'Add a memory system on the bus (proven memory design, bus as persistence)'
status: To Do
assignee: []
created_date: '2026-06-16 22:55'
labels:
  - feature
  - bus
  - memory
  - design
  - 'slug:feat-bus-memory-system'
  - P3
  - ready-for-human
dependencies: []
priority: low
ordinal: 133000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena's idea (2026-06-16): a memory system that lives on the bus — agents record + recall durable lessons/facts as bus artifacts (the bus = the persistence layer), ideally ADOPTING an existing proven memory design (e.g. the Claude/superpowers model: one fact per record + frontmatter + an index + relevance-recall) rather than inventing one. Today the coordinator's memory is LOCAL files (~/.claude/...) — invisible to the operator + not shared across agents/sessions. A bus-native memory makes lessons durable, shared (cross-agent + cross-session), recallable, and operator-visible. The bus artifact store is the persistence; the memory DESIGN (record shape, indexing, recall/relevance) is borrowed from a proven system. Seeded by the first lesson artifact (lesson-review-bottleneck).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Agents record durable memories/lessons as bus artifacts (a memory record shape, from a borrowed design)
- [ ] #2 A recall/relevance mechanism surfaces relevant memories (an index or query over the bus)
- [ ] #3 Memories are shared (cross-agent/session) + operator-visible — not local-only
- [ ] #4 Adopts a proven memory design (record shape + indexing + recall), bus as persistence — not invented from scratch
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Lena's idea (2026-06-16). Borrow a proven memory model (Claude memory / superpowers: one-fact-per-record + frontmatter + index + relevance-recall) with the bus as persistence. Composes with TASK-131 (backlog-on-bus) + TASK-137 (automations). Seeded by lesson-review-bottleneck (the first bus-recorded lesson). Today sirius's memory is local-only (invisible to Lena).
<!-- SECTION:NOTES:END -->
