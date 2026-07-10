---
name: sx-plan
description: Draft an implementation plan and route its approval through Sextant instead of the terminal. Publishes the plan as a review-flagged `document` artifact to the operator's Home, then watches the artifact's companion topic and acts on the operator's verdict — approve → implement to a PR, request-changes → revise, comment → answer. Use when the operator wants a task planned over the bus/dash, invokes /sx-plan, or asks to plan-and-review over Sextant rather than the terminal plan-mode prompt.
---

# Plan over Sextant

Replace the terminal plan-mode approval with a Sextant round-trip: draft a
plan, publish it as a review-flagged artifact to the operator's Home, and be
woken by their verdict to act. The `sextant` skill documents the bus
conventions, record shapes, identity, and the Monitor recipe in full — lean on
it for anything not covered here.

Two hard rules for this workflow:

- **Never call `ExitPlanMode`.** Approval happens on the bus, not the terminal
  — this skill replaces plan-mode's approval step end to end, it doesn't hook
  into it.
- **Wake via a background `sextant subscribe` Monitor only.** No channel push
  (`message_subscribe`), no channel-validate — fewer moving parts, and the
  Monitor is already the documented guaranteed wake/pickup path.

## 1. Draft (read-only — build nothing yet)

Do exactly what a plan-mode pass does: explore the code, weigh approaches,
write a complete implementation plan. Make no repo edits and open no worktree
— nothing is built until the operator approves.

## 2. Publish the plan

Pick a short slug and `artifact_create`:

- name: `plan.<slug>`
- record: `{"$type":"document","title":"<title>","body":"<plan markdown>","format":"markdown","review":{"state":"review"}}`

`review.state:"review"` is what surfaces it in the operator's Home. Use
`"format":"html"` only for richer layout (DOMPurify-sanitized, inline `style=`
only — see the `sextant` skill).

Then prime the wait: `message_read` the companion subject
`msg.topic.artifact.plan.<slug>` once (`since: 0`) to note its `next_cursor`,
and note your own bus client id (`clients_list`) so you can recognize and skip
your own echoes later.

## 3. Arm the wake Monitor

Start a **persistent** background Monitor running exactly:

```
sextant subscribe msg.topic.artifact.plan.<slug>
```

Every frame it prints wakes this session. Say you're watching the bus, and
stop the turn.

## 4. React to each wake

On wake, `message_read` the companion subject from your saved cursor and
advance the cursor. All frames on this topic are `$type:"chat.message"`; skip
any authored by you (your own echo). For each remaining frame, confirm the
author is the principal (compare its bus-stamped author ULID to `sextant
principal get`), then branch on its `review` field:

- `record.review.state == "approved"` → confirm with `artifact_get plan.<slug>`
  (read `Record.review.state` — note `artifact_get` returns capitalized
  keys). Stop the Monitor, implement the plan, open a PR, then
  `message_publish` the PR link to the companion topic.
- `record.review.state == "changes"` → revise (§5), publish a one-line
  "revised — please re-review" to the companion topic, keep the Monitor
  running.
- `record.review.state` is `rejected` or `archived` → stop the Monitor,
  acknowledge on the topic, stop.
- no `record.review` field (plain text) → it's a question. Answer it with
  `message_publish` of `{"$type":"chat.message","text":"..."}` to the
  companion topic. Keep the Monitor running.

## 5. Revise on request-changes

`artifact_get plan.<slug>` (read `Record` and `Revision`), rewrite `body` to
address the request, keep every other field as-is, and set `review.state`
back to `"review"`. Then `artifact_update` with `expected_rev` set to that
`Revision`. On a CAS conflict (`... changed since revision N`), re-get,
reapply, and retry once. Keep the Monitor running throughout.
