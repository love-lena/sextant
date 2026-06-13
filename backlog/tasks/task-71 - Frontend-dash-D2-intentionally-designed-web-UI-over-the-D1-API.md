---
id: TASK-71
title: 'Frontend-dash D2: intentionally-designed web UI over the D1 API'
status: In Progress
assignee:
  - '@orion'
created_date: '2026-06-13 01:12'
updated_date: '2026-06-13 03:35'
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

Progress 2026-06-12: served from dash --serve at / (debug moved to /debug), embedded via go:embed. First cut on seed data, then wired to live D1 API. Live: /api/self, /api/clients to agents+presence, /api/artifacts + document render via marked, SSE on msg.> driving activity feed + conversation discovery, composer publish. Stubbed+labelled: review-status/approve (TASK-66), goal metrics, curated Home greeting/agenda. Verified against the real bus with an agent-browser headless smoke (app mounts, real agents+artifacts render, artifact docs render). Branch worktree-task-71-frontend-dash-d2 (8867ae6 serve, 10f6abe wiring). Preview handed to lena. Pending: lena visual feedback; PR brief + self-validating demo before review; vendor/precompile to drop runtime CDN dep (AC#7).

#1 review loop DONE (commit 7d3dc5d): review-state convention (review block in artifact record; absent=review), POST /api/artifacts/{name}/review (read-merge-CAS, unit-tested), Bus interface += UpdateArtifact, sidebar groups by review-state, approve/request-changes persist + post to companion topic msg.topic.artifact.<name>, Discussion link. Verified full approve flow via agent-browser on a throwaway bus. Open Q for lena: default review-state for un-reviewed artifacts (currently review; she may want draft-until-submitted). Next: #2 curated Home as artifact.

#2 curated Home DONE: Home greeting/agenda/links/note read from a home artifact (assistant-owned) when present, else built-in default; live blocks (pinned/goals/agents/activity) stay live. Created the home artifact on the real bus as orion (rev 25). Verified via agent-browser (curated heading+agenda render, live agents present, old fake greeting gone). Noted: bus Revision is a global write-sequence not a per-doc version; flagged to lena re relabeling the v<n> display.

#3 conversation depth DONE: server subject registry (Server.Watch standing msg.> subscription, GET /api/subjects, unit-tested; wired in serve.go); UI seeds conversations from /api/subjects on load + classifies inbox (msg.client.<id>, one-way) vs DM (msg.topic.dm.<sorted pair>, 2-party, shows other participant) vs topic; click an agent in Agent status to open a DM. Verified end-to-end on throwaway bus. Per lena: DMs are 2-party topics, inboxes are one-way drops.

Review polish DONE: review convention adds rejected+archived states and records review.rev (the revision reviewed against). Header demotes version (updated-time primary + muted 'rev N'; 'approved at vN'); Archive/Reject terminal states with Reopen; archived/rejected drop into their own groups out of the active flow. Verified: handler tests + throwaway smoke. Per lena: also see TASK-72 (sent/received/seen delivery status) — protocol/SDK feature, dash surfaces it later; not faked in D2. Deferred: robust 'changed since approved' stale-flag (needs content-vs-metadata distinction).

Batch fixes (lena nits): (1) hide special-cased home artifact from the list; (2) conversation depth — Watch subscribes with DeliverAll so /api/subjects + conversation list show all retained subjects on load (not just since dash start); (3) conversation view scrolls to bottom on open + on new message; (4) hide/unhide conversations (per-operator localStorage) + 'N hidden — show' toggle. All verified on throwaway bus (replay, home-hide, scroll atBottom, hide). Note: DeliverAll replays full history each start — fine at current scale, may want a stream subject-list API later. task-72 (sent/received/seen) is a separate protocol effort; task-73 (personal-topic/outbox) noted.

HARDENED (PR phase): vendored React/ReactDOM/marked + precompiled JSX via scripts/build-dash-ui.sh (no runtime CDN, no in-browser Babel); --ui hot-reload (Cache-Control:no-store, stable URL). Self-validating demo docs/demos/dash-d2-demo.sh = 7/7. ADR-0033 records the conventions. Full validation green: go test ./internal/... -race, e2e (-tags e2e, 47s), gofumpt+vet clean (make lint noise is the gitignored .claude/worktrees, absent in CI). Opening PR.

Follow-ups filed (per lena): TASK-78 vendor Google Fonts; TASK-79 'changed since approved' staleness flag; TASK-80 goal-metrics real source; TASK-81 live artifact-change stream (vs 4s poll); TASK-82 conversation unread/participant counts. Already tracked: TASK-66 (review convention/CLI), TASK-72 (sent/received/seen), TASK-73 (personal-topic/outbox).
<!-- SECTION:NOTES:END -->
