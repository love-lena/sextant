---
id: TASK-133
title: Native HTML artifacts with inline interaction (richer than markdown)
status: Done
assignee: []
created_date: '2026-06-16 21:27'
updated_date: '2026-06-27 00:05'
labels:
  - wontfix
dependencies: []
priority: low
ordinal: 123000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena's idea (#outbox 2026-06-16): artifacts/briefs today render as markdown (marked + DOMPurify in the dash). She wants the option of NATIVE HTML artifacts with inline interaction — richer, interactive docs (the design-handoff mockups are exactly this: interactive HTML/JS). An artifact could BE an interactive HTML page rather than static prose. KEY design+security depth: arbitrary artifact HTML/JS can't run in the dash's main context (XSS) — needs a sandbox (e.g. sandboxed iframe) and a constrained interaction/messaging model. Decide the rendering+trust model before building.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 An artifact can declare/contain native HTML (not just markdown) and the dash renders it
- [ ] #2 Interactive elements work without compromising the dash (sandboxed; no arbitrary script in the main context)
- [ ] #3 Markdown artifacts keep working unchanged (HTML is an opt-in richer mode)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: #outbox (2026-06-16). Claimed via backlog.counter CAS (133). Design+security call (sandbox model for interactive artifact HTML) -> ready-for-human. Related: the dash artifact render (marked+DOMPurify), the design-handoff bundle (interactive HTML mockups), v0.5 redesign.

Superseded by TASK-222 ([[feat-dash-render-html-artifacts]]), 2026-06-26. TASK-222 delivers the safe HTML-artifact rendering path (content-type marker + DOMPurify/sandboxed-iframe) in the brief reader and artifact view — the concrete, agent-ready slice of this idea. The interactive/JS-with-messaging extension this ticket originally scoped is deferred; if wanted later, file a fresh ticket on top of 222's renderer rather than reopening this.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Won't-do: replaced by TASK-222 (Dash: render HTML artifacts & briefs, sanitized). 222 covers safe HTML rendering — the actionable core of this idea. Interactive/sandboxed-JS artifacts are out of 222's scope and would be a new ticket if revived.
<!-- SECTION:FINAL_SUMMARY:END -->
