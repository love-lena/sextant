---
id: TASK-254
title: >-
  Sandbox read-confinement: evaluate deny-home + allowlist-toolchain vs the
  curated denylist
status: To Do
assignee: []
created_date: '2026-06-30 00:00'
labels:
  - workengine
  - security
  - P2
  - needs-triage
dependencies: []
priority: medium
ordinal: 240000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TASK-118 sandbox mode confines reads via a curated sensitive-path DENYLIST (srt reads default-allow). qa-306 showed a denylist inherently loses the race — it found ~/.tsh (Teleport prod certs), ~/.codex, ~/.claude.json, ~/.cloudflared readable until enumerated. The extended denylist closes the KNOWN stores, but a new cred store appears readable until added. Robust alternative: deny all of $HOME + allowRead only the toolchain paths the worker needs (~/.pi, store, node, etc.) — default-deny — but it starved the toolchain when tried blindly (broke bus connect). Evaluate the deny-home+allowlist posture as a measured iteration (map the worker's real read-set first).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Decision recorded (operator): either (a) deny-home + allowlist-toolchain is adopted with the worker's full read-set mapped and proven not to starve (bus connect + in-scope work + the TASK-98 capstone all pass under it), OR (b) the curated denylist is accepted as the posture with the known cred stores enumerated and the residual documented. Proof: whichever path, a sandbox worker reading an ARBITRARY new ~/.<credstore> is either denied (deny-home) or the residual is explicitly operator-accepted. Flipper: operator. Fake-pass guard: 'added the latest few stores to the denylist' is not the deny-home posture — only a default-deny read model closes the open-ended race.
<!-- AC:END -->
