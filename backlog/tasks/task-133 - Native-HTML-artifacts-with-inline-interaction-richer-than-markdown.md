---
id: TASK-133
title: Native HTML artifacts with inline interaction (richer than markdown)
status: To Do
assignee: []
created_date: '2026-06-16 21:27'
labels:
  - feature
  - dash
  - artifacts
  - security
  - 'slug:feat-native-html-artifacts-inline-interaction'
  - P3
  - ready-for-human
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
<!-- SECTION:NOTES:END -->
