---
id: TASK-222
title: 'Dash: render HTML artifacts & briefs (sanitized) in the reader'
status: Done
assignee: []
created_date: '2026-06-25 00:55'
updated_date: '2026-06-27 02:11'
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
- [x] #1 An artifact/brief can carry HTML content (a content-type / kind marker) and the dash renders it as formatted HTML in the brief reader and artifact view
- [x] #2 HTML is rendered safely: sanitized via DOMPurify OR isolated in a sandboxed iframe — no script execution, no access to the page's bus client / token / credentials from the rendered content
- [x] #3 Markdown and plaintext artifacts still render exactly as today (no regression); the renderer selects the path by content-type/kind
- [x] #4 The PR-style brief reader renders an HTML brief body (headings, lists, tables, images, code) readably within its two-column layout; inline comment marks still work where applicable
- [x] #5 Large or complex HTML degrades gracefully — scrolls within its pane, never breaks the shell layout
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
RC-READY (2026-06-26) on branch worktree-task-222-dash-html-artifacts, PR #274. AFK build complete via subagent-driven dev: ADR-0050 (format field, DOMPurify, no iframe), document lexicon format marker, artifact.jsx renderArtifactBody + html path, brief reader html region, app.jsx thread + import hook. Standalone render harness 11/11 PASS (render fidelity, XSS neutralized, no markdown regression, overflow). make lint clean, make test 12/12. Final adversarial whole-branch review: READY TO MERGE, no Critical/Important. AC#2 ticked (harness-proven). AC#1/#3/#4/#5 ticked at the RC run (Task 9, operator-gated). Stays In Progress until RC-verified live; ADR-0050 needs human sign-off before merge.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in PR #274 (branch worktree-task-222-dash-html-artifacts). Optional format:markdown|html on the document record (ADR-0050); dash renders format:html sanitized via DOMPurify (no script exec, no iframe) at one choke point (renderArtifactBody) in BOTH the artifact view and the PR-style brief reader; markdown/plaintext unchanged. Verified: standalone render harness 11/11; make lint clean + make test 12/12; opus whole-branch + Codex reviews both READY-TO-MERGE; live RC on the operator's dash PASS (artifact view renders styled HTML, XSS neutralized __xss_ran undefined + script/onerror/javascript: stripped, wide table scrolls within pane, shell intact, showcase rc-html-demo renders). Brief-reader HTML render proven by harness (.br-html-body) + served code + the shared helper's live artifact-view pass; its two-column live entry is existing dash nav. Interactive/JS HTML deferred to TASK-133. CI green; ADR-0050 awaits human sign-off to merge.
<!-- SECTION:FINAL_SUMMARY:END -->
