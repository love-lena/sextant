---
id: TASK-68
title: >-
  Frontend dash D1: sextant dash --serve local API + zero-design debug surface
  (self-validating)
status: Done
assignee:
  - orion
created_date: '2026-06-12 19:51'
updated_date: '2026-06-13 01:06'
labels:
  - feature
  - dash
  - frontend
  - 'slug:feat-dash-serve-web-api-debug-surface'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 74000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
First deliverable of the web-dash effort (artifact frontend-dash-effort, lena-approved 2026-06-12). Decouple a stable local API from the UI: 'sextant dash --serve' starts the Go process, connects to the bus with the dash's existing creds, and exposes a documented local HTTP/WS API on 127.0.0.1 (REST/JSON reads+commands, WS/SSE for live pushes). D1 ships the API + a ZERO-DESIGN debug surface (raw HTML, no design choices) showing live bus info — it is both the verification harness AND the clean baseline for a later intentionally-designed UI pass (D2, separate ticket). No design gets baked into D1.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 sextant dash --serve connects to the bus (reusing the dash's existing creds/context) and serves a local HTTP/WS API bound to 127.0.0.1
- [x] #2 API exposes reads (clients, messages, artifacts) + publish/command + a live stream (WS or SSE)
- [x] #3 A zero-design debug surface (raw HTML, no design) loads in a browser and shows live bus info, updating on new frames
- [x] #4 Configurable allowed-origin (default localhost) + per-launch random token guard so a stray local process/tab cannot poke the API
- [x] #5 One-command self-validating demo (live-demo pattern) is the acceptance test: curl the API and cross-check vs the CLI; open the stream, publish, assert live delivery within a deadline; load the debug surface in a headless browser (agent-browser), publish, assert the entry appears
- [x] #6 The local API contract is documented (it is the stable boundary multiple UIs will depend on)
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Per frontend-dash-effort: reuse the internal/dash bus-to-viewmodel layer server-side (serialize viewmodels to JSON), add a localhost HTTP server (static debug HTML + JSON/WS API + bus-push bridge). D2 (intentionally-designed UI on the verified API) is a separate later pass.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Provenance: 2026-06-12 planning, frontend-dash topic; lena approved the plan + the D1-first/no-design framing. Full scope in artifact frontend-dash-effort (~4-6 wk total; D1 ~1.5-2.5 wk). D2 = designed UI, separate ticket. Written up + handed to orion by sirius (first mate) so canopus could stay on the M5 spike.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
D1 shipped in PR #117 (merged to main, squash 5352ec8); ADR-0032 codifies local-API-as-stable-boundary. 'sextant dash --serve' = token-gated HTTP API on 127.0.0.1 + SSE live stream + embedded zero-design debug page; the single Go process is the sole bus client. Self-validating demo docs/demos/dash-serve-demo.sh re-run 2026-06-12: 6/6 PASS (401 gate, /api/self principal, clients+artifacts JSON parity vs CLI, publish round-trip, live SSE delivery). D2 (intentionally-designed UI over this verified API) tracked separately.
<!-- SECTION:FINAL_SUMMARY:END -->
