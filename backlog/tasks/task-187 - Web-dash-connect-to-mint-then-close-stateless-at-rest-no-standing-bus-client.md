---
id: TASK-187
title: >-
  Web dash: connect-to-mint-then-close (stateless at rest, no standing bus
  client)
status: To Do
assignee: []
created_date: '2026-06-23 19:33'
updated_date: '2026-06-23 19:59'
labels:
  - feature
  - dash
  - bus
  - components
  - 'slug:feat-dash-stateless-mint-on-demand'
  - P1
  - ready-for-agent
dependencies:
  - TASK-186
priority: high
ordinal: 177000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The web dash server is one tiny step from stateless. Audit (design session 2026-06-23): the dashapi.Bus interface is now a SINGLE method, MintSession (server.go:43-45). Every piece of SPA data — clients, artifacts, goals, messages, the live stream, publish, review — already routes browser-direct over wss via window.SextantBus (ADR-0044); ZERO Go-relayed endpoints remain in use (the SPA's only HTTP calls are POST /api/session once at boot and the static /build.json). The ONLY thing tethering the Go process to the bus is minting that session credential: POST /api/session -> MintSession -> a clients.session round-trip on the live connection (server.go:245). But serve.go:45 calls sextant.Connect at startup and HOLDS it for the whole process life (defer client.Close, serve.go:57) — a phantom client connected even with zero tabs open. That standing connection is also what threw the sx.hb permissions violation we saw bringing the dash up. Fix: connect lazily, per mint, then close — connection lifetime = one clients.session round-trip, not process life. At rest the dash has zero bus presence; the only connected client the bus ever sees is an open browser tab. This is exactly the 'server-up != client-connected' split, and it makes the keep-alive component ([[feat-dash-managed-component]]) safe.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The web dash holds NO standing bus connection at rest: the Server struct carries no persistent bus client/connection field, and a dashapi test asserts that constructing + starting the server opens zero bus connections (the fake Bus records connect calls; count is 0 until a request arrives). The process-lifetime sextant.Connect/defer-Close in serve.go is gone
- [ ] #2 POST /api/session connects, mints via clients.session, and closes the connection WITHIN the request (a test asserts exactly one connect+close per /api/session call, with the connection closed before the handler returns); opening the dash in a browser still yields a working session credential and a live browser-direct connection
- [ ] #3 With the server up and no browser tab open, no `sx.hb` permissions violation is logged on startup (the standing subscription that caused TASK-185 is gone because there is no standing connection); grep the dash's stderr/log for the violation string and assert absent
- [ ] #4 NO connection cache and NO standing/pooled connection — each mint is a fresh connect→clients.session→close (per ADR-0046). Mint latency is one loopback connect per tab-open; this is accepted, not optimized. Do NOT add speculative caching
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Replace the process-lifetime sextant.Connect in serve.go with a per-request connect inside the MintSession path (a thin minter that Connect→clients.session→Close, or a short-lived pooled connection). Keep the ADR-0044 mint seam intact — this changes connection LIFETIME only, not the mint-on-behalf model.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: design session 2026-06-23 (audit of dashapi Bus dependency). GATED ON ADR-0046 acceptance + [[feat-dash-standalone-binary]] landing. Cites: server.go:43-45 (Bus iface), server.go:245 (MintSession), serve.go:45/57 (standing Connect). Bonus: clears the sx.hb noise tracked in [[bug-creds-sx-hb-subscribe-perms]] (TASK-185). Related: [[feat-dash-managed-component]].
<!-- SECTION:NOTES:END -->
