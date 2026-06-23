---
id: TASK-185
title: >-
  clients list emits NATS permissions violation subscribing to own sx.hb.<id>
  heartbeat subject
status: To Do
assignee: []
created_date: '2026-06-23 18:28'
labels:
  - bug
  - bus
  - presence
  - heartbeat
  - auth
  - creds
  - 'slug:bug-creds-sx-hb-subscribe-perms'
  - P2
  - ready-for-human
dependencies: []
priority: medium
ordinal: 175000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
On v0.6.0, every `sextant clients list` prints to stderr:

    nats: permissions violation: Permissions Violation for Subscription to "sx.hb.<id>" on connection [N]

where <id> is the *caller's own* client id (observed live as the operator lena, sx.hb.01KTXBYMJN8X4FZ8HJPF5XJJ0A). The command still returns the client list (presence renders), so it is non-fatal — but it is noisy on a normal-path command and signals a real permissions gap: the client is being denied a subscribe it apparently needs for heartbeat-derived presence (ADR-0036, the sx.hb.* presence/liveness primitive).

Likely cause (UNCONFIRMED — not yet traced): the operator's creds were minted before the sx.hb.* subscribe grant was added to the cred-minting permission template, so the JWT allowlist permits publishing a heartbeat but not subscribing to the sx.hb.* subjects clients list reads for liveness. A freshly-minted post-v0.6.0 cred may not reproduce — that is the diagnostic tell.

Open decision (why ready-for-human): if the fix is a minting-template change, already-minted creds in the wild (the operator's, every existing agent's) still lack the grant — so there's a migration call (re-mint vs backfill the permission vs make clients list tolerant of the denial). Don't prescribe until that's decided.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Running 'sextant clients list' against the operator's live bus produces NO 'permissions violation' on stderr
- [ ] #2 Root cause confirmed: identify which subject pattern (sx.hb.<id> / sx.hb.*) the client subscribes to and why the cred allowlist denies it
- [ ] #3 Existing already-minted creds (operator + agents) are handled — either they gain the grant via re-mint/backfill, or clients list tolerates the denial gracefully — decision recorded on the ticket
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: post-v0.6.0 brew upgrade dogfooding 2026-06-23 (sextant v0.6.0, f94491104f13). Repro: with the operator's existing pre-heartbeat creds, run 'sextant clients list' and watch stderr; the violated subject is the caller's own sx.hb.<id>. Related: [[feat-heartbeat-presence-primitive]] (TASK-126), heartbeat shipped via ADR-0036/#162 in v0.5.0; [[project_v060_shipped]].
<!-- SECTION:NOTES:END -->
