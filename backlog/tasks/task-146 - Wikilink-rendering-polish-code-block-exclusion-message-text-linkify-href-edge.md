---
id: TASK-146
title: >-
  Wikilink rendering polish: code-block exclusion + message-text linkify + href
  edge
status: To Do
assignee: []
created_date: '2026-06-17 00:42'
labels:
  - feature
  - dash
  - wikilink
  - 'slug:feat-wikilink-render-polish'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 136000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Follow-ups from PR #169 (4c8d09b, [[wikilinks]] in artifact bodies) surfaced by codex's review. Lena confirmed [[wikilinks]] as a standing convention for artifacts AND messages (weak: invalid is fine). #169 shipped artifact-BODY rendering (valid=link, invalid=muted). Remaining gaps: (1) the body renderer runs a regex over marked's HTML string without code-context, so [[name]] inside a fenced/inline code block linkifies instead of staying literal; (2) message TEXT does not linkify [[ ]] at all yet (messages render plain text + a structured artifactRef button only) — the convention should work in messages too, same valid=link/invalid=muted treatment; (3) a wikilink used as a markdown link href (e.g. [t]([[x]])) yields malformed-but-inert HTML (DOMPurify strips it). All non-security (codex confirmed #169 XSS-safe).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 [[name]] inside fenced or inline code blocks renders as literal text, not a link
- [ ] #2 Message text linkifies [[name]] (valid=clickable in-dash link, invalid=muted+inert), matching the artifact-body behavior
- [ ] #3 A wikilink in a markdown link href degrades gracefully (no malformed HTML), or is explicitly out of scope with a note
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Body: make renderWikilinks code-region-aware (skip <pre>/<code> segments, or run on text nodes). Messages: share the same escape+linkify helper from artifact.jsx in the message renderer (sidebar.jsx, orion's file) — extract a small shared util. Re-run codex on any change to the escape/splice path (XSS-sensitive).
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: PR #169 codex review (4c8d09b). Edges are cosmetic/non-security. Shared util means orion (sidebar.jsx) + the artifact.jsx path coordinate. Related: the wikilink convention (Lena 2026-06-16).
<!-- SECTION:NOTES:END -->
