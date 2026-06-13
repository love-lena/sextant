---
id: TASK-71
title: 'Frontend-dash D2: intentionally-designed web UI over the D1 API'
status: In Progress
assignee:
  - '@orion'
created_date: '2026-06-13 01:12'
updated_date: '2026-06-13 01:12'
labels:
  - feature
  - dash
  - frontend
  - ui
  - 'slug:feat-dash-web-ui-d2'
  - P2
  - ready-for-agent
dependencies:
  - TASK-68
priority: medium
ordinal: 76000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
D2 of the web-dash effort (artifact frontend-dash-effort): implement the minimalist cockpit UI lena designed in Claude Design and handed off (bundle: Sextant App.html + 5 chat transcripts), served by sextant dash --serve over the verified D1 local API (ADR-0032, TASK-68). Landed design: an artifact-hero stage that swaps between a curated never-scroll Home (bento dashboard the assistant maintains), a rendered-markdown artifact review (approve / request-changes), and a full conversation view; plus a splittable/accordion sidebar navigator (Conversations+DMs unified, Artifacts grouped by status, Goal progress, Agent status). Minimalist visual language (final chat5 pass): white surfaces, hairline borders, a single green live accent, black primary actions, Hanken Grotesk. Build visual-first (the prototype renders on seed data) served by dash --serve, then wire each panel to the D1 API incrementally against lena eye.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The designed UI is served by sextant dash --serve at / (the Config.UIDir / embedded-assets hook), token-gated exactly like D1; the zero-design debug surface stays reachable (e.g. at /debug)
- [ ] #2 Sidebar navigator: Conversations+DMs unified, Artifacts grouped by status, Goal progress, Agent status, with accordion (sections) and tabs modes per the design
- [ ] #3 Stage swaps between Home (curated never-scroll bento), markdown artifact review (approve / request-changes), and a full conversation view, matching the handoff
- [ ] #4 Live data wired to the D1 API: /api/self, /api/clients to agents (with presence), /api/artifacts (+ /api/artifacts/{name}), /api/messages per topic/DM, POST /api/publish (composer), GET /api/stream SSE (live) — replacing the prototype seed data
- [ ] #5 Concepts with no bus primitive are stubbed with a documented backing plan: artifact status + approve + companion-topic per feat-brief-workstream-convention (TASK-66); goal metrics; the curated Home
- [ ] #6 Self-validating demo (live-demo pattern) drives the UI against a throwaway bus; a PR brief artifact (pr-N-brief) is written before lena reviews
- [ ] #7 Merged result matches the final visual language and carries no runtime CDN dependency (vendor or precompile React+JSX or an equivalent build-free approach)
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Embed the designed UI assets in internal/dashapi (go:embed); serve at / with the debug surface at /debug; UIDir overrides for dev. First cut = pixel-faithful design on seed data served by dash --serve (fastest to lena eye). Then replace seed constants with fetches to the D1 endpoints panel by panel; add a live-demo + brief. Status/approve/companion-topic back onto TASK-66 when it lands.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Provenance: 2026-06-12 frontend-dash topic; lena handed off the Claude Design bundle (Sextant App.html + chats) and said implement the relevant aspects. Bundle extracted to /tmp/sextant-design/sextant-webapp (chats carry the intent; chat5 is the final minimalist pass). Builds on [[feat-dash-serve-web-api-debug-surface]] (TASK-68, D1, ADR-0032). Artifact status/approve/companion-topic back onto [[feat-brief-workstream-convention]] (TASK-66, ready-for-human, unstarted). Assigned orion.
<!-- SECTION:NOTES:END -->
