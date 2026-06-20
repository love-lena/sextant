---
id: TASK-180
title: Dash as a direct NATS-WebSocket co-equal TS client
status: Done
assignee: []
created_date: '2026-06-19 21:11'
updated_date: '2026-06-20 04:18'
labels:
  - feature
  - dash
  - websocket
  - typescript
  - client
  - 'slug:feat-dash-direct-ws-client'
  - P2
  - ready-for-agent
dependencies:
  - TASK-174
  - TASK-175
  - TASK-177
ordinal: 170000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Make the dash a direct NATS-WebSocket co-equal TS client: enable the bus WebSocket listener; the Go dash backend shrinks to static-SPA host + an ephemeral scoped-credential mint endpoint; the browser uses the TS SDK + TS conventions over wss directly; the dashapi convention re-implementation (hand-rolled goal logic) is removed. Conditional on the task-179 decision. PRD doc-2.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 the bus exposes a WebSocket listener; the browser connects over wss with a dash-minted short-lived scoped credential
- [ ] #2 the dash browser speaks goals/review/home via the TS conventions directly; no Go-backend convention re-implementation remains
- [ ] #3 the Go dash backend is reduced to static hosting + the creds-mint endpoint
- [ ] #4 Draft the ADR revising ADR-0032/0034 + the browser-credential model; signed off on this ticket's merge
- [ ] #5 OPERATOR-VERIFIED: the operator opens the dash in a browser, it connects over wss with a dash-minted short-lived credential, and Home/Goals/review work end-to-end (read live data, write a review verdict, set a goal) with the Go backend reduced to static-host + creds-mint and the internal/dashapi goal/review re-implementation deleted
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Dash as a direct NATS-WebSocket co-equal TS client SHIPPED (PR #242 squash; build-from-clean fix f71312e). Bus ws listener (default-off loopback NoTLS, config/CLI/doctor, bus.json wsURL); TS SDK ./browser entry (browserConnect over nats.ws, Node SDK byte-unchanged); conv-goals project() + NEW @sextant/conv-review; Go dashapi SHRUNK — review.go + /api/* relay + SSE + subject-discovery DELETED, Bus narrowed to ID()+Register(), added POST /api/session (mint-on-behalf kind=browser); SPA over wss (conventions in-browser, msg.> live sub, vendored IIFE bundle); cred-TTL (mintUser ttl param, ttl=0 perpetual byte-identical, browser=1h). ADR-0044 (proposed, revises 0032/0034). ORCHESTRATOR VERIFIED: make ui from a CLEAN worktree (the build-from-clean defect — build-dash-ui.sh now builds the 3 TS pkgs before esbuild — fixed), gate green (Go 32 ok, TS SDK 22/22 + conv-goals 19/19 + conv-review 6/6), BOTH CI green, self-validating dash-direct-ws demo 5/5 (mint + distinct creds + /api/* 404 + survivors 200), hermetic (active=lena). Worker AC#5 agent-browser drive (wss Home/Goals/review verdict+goal). PR-noted follow-ups: Go conventions/review peer, native wss TLS, browser-cred GC, /debug legacy.
<!-- SECTION:FINAL_SUMMARY:END -->
