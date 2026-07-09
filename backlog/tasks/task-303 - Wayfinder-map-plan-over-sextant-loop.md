---
id: TASK-303
title: 'Wayfinder map: plan-over-sextant loop'
status: To Do
assignee: []
created_date: '2026-07-09 20:10'
labels:
  - 'wayfinder:map'
dependencies: []
ordinal: 232000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Wayfinder map (label wayfinder:map). Child tasks are its tickets; a child's --dep links are its blockers; the FRONTIER = open + unblocked + unassigned children; assign a child to yourself to CLAIM it. Refer to tickets by name, never bare id.

## Destination
The /plan skill, over Sextant. A manned Claude Code session A in the devbox drafts a plan, auto-publishes it to Lena's Sextant inbox with NO manual step, and is woken by her review to act: approve -> implement through to a PR; questions -> answer in the artifact chat and keep monitoring; request-changes -> revise the artifact and keep monitoring. Built on a bankrupted backlog + ADR set (keep only ADR-0001 vision + CONTEXT.md). Value test: Lena can use Sextant for real work WITHOUT ever merging the work engine.

## Notes
- This effort CARRIES EXECUTION (overrides wayfinder's plan-only default): the destination is a working loop live on Lena's setup, so the map terminates in a built, live-verified, merged loop, not just decisions.
- Bright lines to hold: two primitives only (artifact + message); signal-not-manage; call-functions-not-manage-processes; engine-as-library-never-in-core. NO coordinator / dispatcher / runs / steps: that machinery IS the work engine, which this effort RETIRES (rc/work-engine; ADRs 0048/0051/0052), never merges.
- The loop is mostly WIRING PROVEN PRIMITIVES: review-state convention + setReview (conventions/review/ts); artifact companion-topic chat (msg.topic.artifact.NAME, rendered in review.jsx rail + pop-out); the Home/inbox review projection (home.jsx); the channel-push wake path (clients/sextant-mcp/channel.go + the startup skill). Net-new is the /plan skill/loop itself + proving the wake is reliable.
- Skills each session should consult: /grilling, /domain-modeling, /prototype, go-house-style (Go), the sextant skill (bus conventions). Keep tickets SHORT: one sharp question, a few lines (Lena: tasks are too long).
- Tracker mapping (this repo = Backlog.md): map = task labelled wayfinder:map; tickets = child tasks (--parent) labelled wayfinder:TYPE; blocking = --dep; claim = --assignee.

## Decisions so far
(none yet)

## Not yet specified
- Build the /plan skill = the draft + auto-publish + monitor + act loop (graduates once the wake mechanism, the auto-publish trigger, and the plan-artifact/review contract are settled).
- Live-verify the loop end-to-end on the devbox (graduates once the skill exists).
- Session A's devbox environment: identity, creds, context, and the channel research-preview flag (graduates once the wake mechanism is chosen).

## Out of scope
- Building on or merging the work engine (rc/work-engine run-executor, coordinator, dispatcher; ADRs 0048/0051/0052): consciously retired. Archiving its tickets rides the bankruptcy.
<!-- SECTION:DESCRIPTION:END -->
