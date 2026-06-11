---
id: TASK-34
title: e2e harness leaks embedded nats-server children
status: To Do
assignee: []
created_date: '2026-06-09 19:15'
updated_date: '2026-06-11 00:02'
labels:
  - ready-for-agent
dependencies: []
ordinal: 40000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Observed 2026-06-09: ~33 orphaned 'nats-server -js -c <tempdir>/sxtd*/nats/nats-server.conf' processes accumulated from e2e/integration runs (temp configs under /tmp and $TMPDIR), surviving after test exit. The harness (or 'sextant up' under test) spawns an external nats-server child and teardown doesn't kill it on every path. Fix: ensure the harness reaps the child (process group kill / t.Cleanup with SIGKILL fallback), and add a leak check (no nats-server children after suite). Cleanup of current orphans: kill PIDs from: ps -eo pid,command | grep 'nats-server -js -c' | grep -E '/(tmp|var/folders)/'
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-06-10: orphan count was 33 (grew with today's PR #99 gate runs); all killed via the recipe. Root cause still unfixed — the harness still leaks on some path.
<!-- SECTION:NOTES:END -->
