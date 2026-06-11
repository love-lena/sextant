---
id: TASK-39
title: SDK subscription dies silently across a bus restart
status: Done
assignee: []
created_date: '2026-06-10 03:25'
updated_date: '2026-06-10 23:58'
labels:
  - ready-for-agent
dependencies: []
priority: high
ordinal: 45000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Hit live 2026-06-09 (twice-burned day for live-tail): a client held a Subscribe on msg.> across a bus restart. The NATS connection auto-reconnected (ADR-0025 stable port made the address valid again) and publishes kept working, but the JetStream consumer was ephemeral and died with the old server process — the SDK neither re-established it nor surfaced an error. The subscriber read nothing for ~4h while believing it was live; the human noticed before the agent did. Fail-loud bright-line violation. Fix shape: on reconnect the SDK should re-establish active subscriptions (resuming from last-delivered sequence where the stream allows) OR tear down with a loud error to the Handler so the caller can resubscribe — never silence. Also feeds TASK-22 learning #2 (the MCP server must own the live-tail problem). Repro: subscribe, restart 'sextant up' on the same store, publish — handler receives nothing, no error.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented on feat/dash @ c7d7059 + 1641dee (review fixes): per-subscription lastSeq (atomic, stream sequence, stored pre-handler), reconnect handler re-establishes every active sub via stop-stale-relay-then-resubscribe from lastSeq+1 (survives restarts AND plain blips AND flapping); impossible resume (wiped store, retention past lastSeq via FirstSeq bound) dies loudly through the new OnError SubOption, wired to busfeed.ErrMsg (events drained before terminal error). since_seq additive on the wire (epoch 1). ADR-0027. Red-green proven both Majors (TCP-proxy blip repro; real JetStream purge for the FirstSeq bound). Closes with PR #99 merge.

Fixed in: 4887258 (PR #99)
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in PR #99 (squash 4887258): subscriptions survive restarts AND blips — relay generations + per-resume sub-id rotation + monotonic cursor give exactly-once; two-tier resume failure (fatal vs ErrResumeDeferred); resume pass runs off the NATS dispatcher with one-pass-per-token and bounded Close drain. ADR-0027 records the contract. Follow-ups: TASK-40/41/44/45.
<!-- SECTION:FINAL_SUMMARY:END -->
