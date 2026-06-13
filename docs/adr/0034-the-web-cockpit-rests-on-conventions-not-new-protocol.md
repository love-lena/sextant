---
status: proposed
date: 2026-06-12
---

# The web cockpit rests on conventions, not new protocol

[ADR-0032](0032-the-web-dash-is-a-face-on-a-local-api.md) settled where the
browser meets the bus: `sextant dash --serve` runs the dash's one bus identity
as a local HTTP API on 127.0.0.1, and the browser is a face on that API. D1
shipped that API plus a zero-design debug surface. D2 (TASK-71) is the
intentionally-designed **cockpit** on the same boundary — an artifact-hero stage
that swaps between a curated Home, a markdown artifact review, and a
conversation, with a splittable sidebar navigator. The visual design came from a
Claude Design handoff and is the operator's to iterate.

Building it needed four behaviors the core protocol does not provide: a
**review-state** on an artifact, a **per-artifact discussion**, real
two-party **DMs**, and **conversation discovery** on load. This ADR records that
each is added as a *convention layered over the existing primitives* — the core
artifact and message operations (`methods.json`) are unchanged. That keeps the
core small ([ADR-0022](0022-parallel-modules.md)) and lets the CLI and any other
client adopt the same conventions without a protocol change.

## How it is served

The cockpit is embedded in the binary (`go:embed web/app`) and served at `/`;
the zero-design debug surface moves to `/debug`. The JS is vendored locally
(React, ReactDOM, marked) and the JSX components are precompiled to plain JS by
`scripts/build-dash-ui.sh`, so the served page needs neither a runtime CDN nor
in-browser Babel. `dash --serve --ui <dir>` serves a live directory with
`Cache-Control: no-store` instead, so a browser refresh hot-reloads UI edits
during iteration — a stable URL, no rebuild. (Google Fonts is still loaded from a
CDN; it is cosmetic and degrades to system fonts, and full font vendoring is a
follow-up.)

## The conventions

**Review-state (the brief workstream, [TASK-66]).** An artifact carries its
review-state as a `review` block in its record — `{state, by, at, rev}` — where
`state` is one of `review · approved · changes · draft · rejected · archived` and
`rev` is the artifact revision the review was made against. An artifact with no
`review` block reads as `review` (awaiting you). It is set by
`POST /api/artifacts/{name}/review`, which reads the record, merges the block,
and compare-and-sets it; the core create/get/update/list operations are
untouched. The UI groups artifacts by this state and offers Approve / Request
changes / Archive / Reject (with Reopen for the terminal states).

**Per-artifact discussion.** Every artifact has a companion topic derived from
its name, `msg.topic.artifact.<name>`. Approve / request-changes post an event
there, and the artifact view links to it.

**DMs are two-party topics.** A direct message is a topic shared by exactly two
clients, with a canonical subject from the sorted pair:
`msg.topic.dm.<id-a>.<id-b>`. This is distinct from a client's **inbox**,
`msg.client.<id>`, which is a one-way drop, not a back-and-forth conversation.
(Confirmed bus-wide while wiring this up.)

**Conversation discovery.** `dash --serve` holds a standing `msg.>` subscription
that replays history (`DeliverAll`) and records every subject it sees;
`GET /api/subjects` lists them so the UI can populate its conversation list on
load, not only as new traffic arrives.

## What stays out

Some of the design's surface has no bus primitive yet, and is deliberately not
faked: **goal metrics** are a stub; the curated **Home** is itself an artifact
the assistant owns (`home`, special-cased and hidden from the artifact list);
per-message **sent / received / seen** status is a protocol/SDK effort tracked
separately ([TASK-72]). A reliable "changed since approved" flag is also deferred
— the review write bumps the artifact revision, so distinguishing a content
change from a review-metadata change needs more than a revision compare.

## Consequences

The cockpit can grow without protocol churn, and the review/DM/discovery
conventions are available to every client, not just the dash. Review-state lives
in the record, so writing it bumps the revision — acceptable, and the basis for a
later staleness signal. Precompiling drops the multi-megabyte in-browser Babel
and keeps the binary lean. The boundary from ADR-0032 holds: the API contract is
the stable thing; the cockpit is a swappable face on it.

Links: [TASK-71] (this work), [TASK-66] (the review/brief convention),
[ADR-0032] (the local-API boundary), [TASK-72] (delivery status, separate).
