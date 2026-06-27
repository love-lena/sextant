---
id: TASK-222
title: 'Dash: render HTML artifacts & briefs (sanitized) in the reader'
status: In Progress
assignee: []
created_date: '2026-06-25 00:55'
updated_date: '2026-06-27 00:05'
labels:
  - 'slug:feat-dash-render-html-artifacts'
dependencies: []
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant.html
priority: medium
ordinal: 211000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Agents and the operator increasingly produce rich HTML documents (reports, roadmaps, mockups, dashboards) — but the dash renders artifacts/briefs as markdown/plaintext, so HTML content isn't shown as intended. Let an artifact or brief declare HTML content and have the dash render it SAFELY in the brief reader and artifact view, alongside the existing markdown path. Content stays opaque to the substrate (bright line); rendering is a client concern. Reuse the existing DOMPurify path or a sandboxed iframe.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 An artifact/brief can carry HTML content (a content-type / kind marker) and the dash renders it as formatted HTML in the brief reader and artifact view
- [ ] #2 HTML is rendered safely: sanitized via DOMPurify OR isolated in a sandboxed iframe — no script execution, no access to the page's bus client / token / credentials from the rendered content
- [ ] #3 Markdown and plaintext artifacts still render exactly as today (no regression); the renderer selects the path by content-type/kind
- [ ] #4 The PR-style brief reader renders an HTML brief body (headings, lists, tables, images, code) readably within its two-column layout; inline comment marks still work where applicable
- [ ] #5 Large or complex HTML degrades gracefully — scrolls within its pane, never breaks the shell layout
<!-- AC:END -->
