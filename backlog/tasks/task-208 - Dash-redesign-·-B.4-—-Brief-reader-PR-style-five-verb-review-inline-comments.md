---
id: TASK-208
title: >-
  Dash redesign · B.4 — Brief reader (PR-style) + five-verb review + inline
  comments
status: Done
assignee: []
created_date: '2026-06-24 01:08'
updated_date: '2026-06-25 02:31'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-review
dependencies:
  - TASK-220
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: high
ordinal: 198000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A document brief reviewed like a pull request; the decision is earned at the end of reading, never from a preview. Document-only — §13 quick-decision is NOT built. Parent: EPIC B (task-199). Covers AC §12.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S12.1 two-column layout: document left, comments rail right; top bar shows run ULID + goal
- [ ] #2 S12.2 document shows type label, stream, title, byline (which run authored it; whether revised after notes; read-effort), a Why-you're-seeing-this callout, optional numbered plan, and the body
- [ ] #3 S12.3 body blocks carrying a note show an inline comment mark; clicking toggles the associated comment active in the rail
- [ ] #4 S12.4 comments rail lists comments (anchor quote, author, time, text) each with Reply that appends operator replies inline; empty reads No comments yet
- [ ] #5 S12.5 rail footer review action: a comment input + verbs Approve, Request revisions, Request answers, Reject, Ignore; a type-specific prompt frames the decision
- [ ] #6 S12.6 submitting any verdict closes into the review-done consequence (§15 / B.5); rail collapsible to a spine (sextant.rail.collapsed.v1)
- [ ] #7 S12.7 an Activity section logs the brief's events (pushed/approved/changes) tagged by kind, source ULID, time
- [ ] #8 Persistence/proof: submitting a verdict publishes a durable review/decision message (verb + comment) on the brief's topic; after reload the brief shows its resolved/closed state, re-read from JetStream — not local state
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in v0.8.0 (dash redesign; tag 275522a, 2026-06-24) — built across 5 parallel lanes, integrated on dash-redesign-demo, persona-swept, design-fidelity audited 0/0/0, reviewed live, released + verified on the managed dash (:8765).
<!-- SECTION:FINAL_SUMMARY:END -->
