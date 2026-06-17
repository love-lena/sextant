---
status: proposed
date: 2026-06-16
---

# The assistant is a convention, not a primitive

The v0.5.0 press release ships two promises that are really one relationship —
*"your time is actively defended"* (an agent that stands guard over the
operator's attention) and *"a helper you just message"* (the operator's own
agent, a message away). This ADR records the decision (TASK-138, TASK-144,
TASK-120): unify them as **violet**, the operator's assistant, and build violet
as a **convention layered on the existing primitives** — clients, artifacts, and
messages — exactly as [ADR-0035](0035-the-goal-bus-primitive.md) made the goal a
convention rather than a new bus construct. The thin universal core is unchanged.

A designated assistant is not a new kind of client and not a new operation. It is
a client like any other (it dogfoods the protocol), distinguished only by a
**role prompt** and a **designation artifact** the rest of the workspace reads.
The bus does not learn what an assistant is; like every record, the content is
opaque to it ([primitives, not policy](../../CONTEXT.md)). Nothing in the core
protocol changes, the same way the cockpit
([ADR-0034](0034-the-web-cockpit-rests-on-conventions-not-new-protocol.md)) and
the goal ([ADR-0035](0035-the-goal-bus-primitive.md)) added none.

## The assistant is a designated client run as an agent

Violet connects to the bus like orion, canopus, or vega — a registered client
with its own identity. What makes it *the assistant* is two conventional pieces,
nothing core:

- a **role prompt** that gives it its two duties (below) — it is a client driven
  by a prompt, not a new client type;
- the **`assistant` designation artifact** that names which live client is the
  assistant right now (next section).

Because violet is a plain client, it is **swappable** — re-point the designation
at a different client and the assistant changes, with no code change anywhere
that reads it. It sees exactly what the operator sees (all artifacts, all goals,
all `agent.status`, all public topics); it holds no privileged tier and no
operator authority.

## The assistant has two duties: answer, and defend

**Duty 1 — Answer (read-only).** Violet follows its own inbox
(`msg.client.<violet>`). On a question — where a goal stands, what is blocking
the operator, dig something up — it reads the workspace and replies on the
operator's DM. It is **read-only**: it never merges, never changes another
client's artifact or a review verdict, never acts on the operator's behalf. *A
helper that answers is categorically not an agent that acts* — that line keeps it
safe and keeps it distinct from the work crew.

**Duty 2 — Defend (curate the operator's Home/inbox).** On bus activity (and a
periodic tick) violet curates the operator's Home projection so the "Needs
you" view holds only the **real calls**, ranked, with exactly **one clear top
action** (TASK-120), and everything that handled itself collapses to a single
quiet line. The curation judgement is recorded in full in the locked design
artifact `violet-curation-design`; the load-bearing shape:

- **Candidate pool** — artifacts with `review.state = review` (the producer's
  *intent* to surface, per the
  [Needs-review](../../CONTEXT.md) convention), goal criteria marked
  `waiting-on-you`, and question-messages addressed to the operator. Context and
  working artifacts, agent-to-agent chatter, and already-verdicted work are not
  candidates.
- **The test** — *only-you-ness* (is this the operator's to decide — a verdict
  only she can give, a design fork, a question to her, a goal criterion needing
  her sign-off — not merely "important") **and** *effective-use-of-her-time* (is
  it a good use of her attention as presented — is the ask concise, crisp, easy
  to read). Both must lean yes to surface.
- **Output** — ranked real calls, one clear top action (default priority:
  blocks-the-most-downstream-work first, tunable), each carrying a specific
  *"why you're seeing this"* rationale, plus the one quiet line ("N things
  handled themselves").

## Signal-not-manage: violet curates the projection, never a verdict

Violet curates the **view** (the operator's Home/inbox projection); it never
writes a verdict, never overrides an artifact's `review.state`, and never touches
an owner's state. The boundaries:

- **Down-rank, never hide.** An over-eager `review` flag is moved off the top
  view into the quiet line — observable and expandable, never deleted. The
  artifact's own `review.state` is untouched; only the *view* changes. The
  operator and owners stay authoritative (TASK-144 AC#3), and anything
  mis-ranked is re-surfaceable.
- **Un-orphan, don't manage.** Violet has decent authority to change the
  review-status of a **stalled** flagged artifact — one whose author flagged it
  `review` and then went quiet — so it does not rot in the inbox forever. This is
  housekeeping on abandoned flags, always auditable and correctable; it is not
  authority over live, owned work.
- **Constructive pushback, not nudging.** When an item is not yet worth the
  operator's attention, violet comments **on the item** — "I don't think this
  needs Lena yet because XYZ" — actionable for the author to sharpen the ask or
  resolve it without her. It is a signal to the producer, never enforcement. The
  root fix for inbox noise is producers setting `review` only for genuine
  for-operator judgement; violet is the second line.

This is the
[goal design](0035-the-goal-bus-primitive.md)'s curation layer made real:
programmatic projection → intelligent curation → human attention. It is the same
discipline the goal primitive holds — *signal + cooperate, never track +
manage*: violet reports and curates a projection; it never directs a client or
overrides an owner.

## The `assistant` designation artifact

Who the live assistant is, is itself a bus record — not a hardcoded id. The
**`assistant`** latest-value artifact holds:

```
{ client_id, name: "violet", accent }
```

- `client_id` — the bus identity (ULID) of the live assistant client;
- `name` — the assistant's display name (`"violet"`);
- `accent` — the assistant's identity colour, used by the dash (the violet FAB
  accent, the DM thread).

The dash and the crew read `assistant` to know who the assistant is and how to
reach it (its DM is the FAB panel; `⌘K` no-match → "Ask the assistant" → DMs this
client). Because the assistant is named by a swappable record rather than a
constant, the operator can re-point it at a new client without touching any
consumer. This mirrors the goal/status pattern — a latest-value artifact other
clients project from — and adds **zero new operations**.

This ADR specifies the convention only. A live `assistant` artifact is created at
**release** (when violet goes live on the operator's bus, after sign-off — see
"When it goes live"), not at build time.

## When it goes live

The reference runtime — a long-lived client driven by violet's role prompt (the
SDK runtime is a separate later PR) — runs on the operator's **live** bus only at
v0.5.0 **release** (operator sign-off + tag). Until then the assistant convention
is built and validated on the `v0.5` branch. Standing up an always-on new agent
on the live bus is a release-time commitment, not a build-time one.

## Consequences

- The thin core is untouched: the assistant is two lexicons-of-convention (the
  role and the designation artifact) over the existing client, artifact, and
  message operations — zero new operations, the same way ADR-0034 and ADR-0035
  added none.
- The dash de-stubs its assistant surface (the FAB panel and the `⌘K`
  no-match → "Ask the assistant" entry the reskin already built) by reading the
  `assistant` artifact for the live client id and accent, and wiring the FAB to
  that client's DM — wiring, not new flow.
- Home/inbox curation moves from the coordinator (sirius curates Home today) to
  violet at v0.5.0; the coordinator returns to pure coordination and gating. No
  double-owner on Home.
- The assistant is swappable by re-pointing one artifact; we abstract against a
  second assistant only if one ever exists
  ([abstract only against a second implementation](../../CONTEXT.md)).
- We deliberately do **not** make the assistant a core concept, a privileged
  tier, or a new operation; if enforcement (an assistant that could *act* on the
  operator's behalf, or gate work) were ever wanted, that would be a new,
  separately-argued decision — it is explicitly *out* here. The assistant stays
  read-only and signal-only.

Links: TASK-138 (the assistant you message), TASK-144 (the attention-defender),
TASK-120 (one clear next action),
[ADR-0035](0035-the-goal-bus-primitive.md) (the convention-not-primitive
precedent and the curation layer this realizes),
[ADR-0034](0034-the-web-cockpit-rests-on-conventions-not-new-protocol.md) (the
cockpit conventions and the dash surface this de-stubs),
[ADR-0030](0030-clients-act-on-a-principals-messages-as-operator-input.md) (the
principal/authority model the assistant deliberately does not hold), and the
approved `v0.5.0-press-release` sections "Your time is actively defended" and
"A helper you just message".
