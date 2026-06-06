---
id: TASK-16
title: Credential reissue / revoke flow for client names
status: To Do
assignee: []
created_date: '2026-06-03 22:59'
labels: []
milestone: Open design questions
dependencies: []
references:
  - pkg/bus/auth.go
priority: medium
ordinal: 16000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
MintClient / `sextant token` now hard-error on a duplicate client id (issued-names ledger at <store>/issued, added with the SDK-connect work) to kill the silent-collision footgun. Consequence: a lost or rotated creds file leaves that id permanently stuck — there is no supported way to reissue. Design and add an escape hatch.

Wrinkle to resolve: NATS user JWTs here have no expiry and the account configures no revocation list, so naive 're-mint under the same name' would create a SECOND valid credential (the old one still authenticates) — reintroducing the very collision the ledger prevents. A real reissue therefore needs credential revocation (account JWT revocation list) or short-lived creds + renewal, not just deleting the ledger entry.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A supported way to revoke+reissue a client id (decide mechanism: `token --force`, a `revoke` verb, or short-lived creds + renewal)
- [ ] #2 Reissuing invalidates the prior credential for that id (old creds stop authenticating), not merely mints a second one
- [ ] #3 Ledger and any revocation state stay consistent under the chosen mechanism
<!-- AC:END -->
