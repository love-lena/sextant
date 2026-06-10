---
status: proposed
date: 2026-06-08
---

# The dash is three master-detail browsers

The dash is a cockpit of three **browsers** — `clients`, `topics`, and
`artifacts` — each a list of what exists that you step into to read or
participate. This refines [ADR-0023](0023-the-dash-is-a-composable-pane-cockpit.md):
the composable pane-cockpit stands, but dogfighting the first M4 build showed the
operator wants to **see everything there is and open any of it in place**, rather
than launch bound to one fixed topic and one fixed artifact. So each pane gains a
top-level list, and a pane's detail opens **inside the same pane**.

**A browser is a list you step into.** Each of the three panes shows a list at
rest — every client, every topic, every artifact. `Enter` on a row opens that
row's **detail in the same pane**; `Esc` returns to the list. This nests under the
two-level focus already in the layout: the layout selects a pane, `Enter` steps
into its list, `Enter` again opens a detail, and each `Esc` pops one level back
out. The layout's focus model is unchanged — list-versus-detail is a surface's own
state, so the layout still composes plain panes. This **is** detail-on-demand
(ADR-0023): the detail is reached on demand and is never an always-on column —
realized within each browser rather than as a separate floating pane.

**The three browsers, and where their lists come from.**

- **clients** — the directory (`clients.list`, already the presence view).
  `Enter` opens a **direct conversation** with that client on its direct subject
  (`msg.client.<id>`). A direct message and a topic room are the same conversation
  surface over different subjects — addressing is a subject-level convention, not
  a fork (ADR-0023).
- **topics** — discovered **client-side**: the dash subscribes to `msg.topic.>`
  (replaying history), and the list is every topic with retained messages,
  demultiplexed by subject. There is no topic registry, and the bus grows no new
  state to maintain — the topics are simply the subjects the durable messages
  stream already holds, and the client derives the list from **its own
  subscription** (ADR-0012: the messages space is yours; a topic exists because it
  has messages). Opening a topic by name starts participating in it; `Enter` on a
  row opens its conversation.
- **artifacts** — discovered via a new **`artifact.list`** read verb: it returns
  the names + metadata the `ARTIFACTS` bucket already holds (the bus owns that
  bucket — ADR-0016 — so listing its keys is discovery of existing state, the same
  shape as `artifact.get`, not a new construct). `Enter` opens the document reader,
  kept live by `artifact.watch`.

**Conversations and documents are the details, unchanged.** The message surface
stays exactly as ADR-0023 set it: one read-stream plus an optional compose, the
two merging by round-trip with no optimistic echo. It is now the detail of both a
topic and a client (a DM). The artifact reader is the detail of an artifact. The
browsers add discovery and in-place navigation around the same two detail
surfaces; they do not change them.

**Launching is `sextant up` then `sextant dash`.** The dash is still just a client
(ADR-0014), but a first run should need no ceremony: when a bus is reachable by
discovery and the caller has no identity yet, the dash **enrolls itself**
(`clients register --self`, named from `$USER`, overridable) and announces it in
one line, then opens the cockpit. An existing context is used as-is; with no bus
reachable it says to run `sextant up` first. Enrollment stays a client act over
the SDK (ADR-0021) — the bus is not special-cased.

Map (ADR-0003): the dash (a human-UI client) and its three browsers over the SDK —
`clients.list` + `msg.client.<id>`, the `msg.topic.>` subscription, and
`artifact.list` + `artifact.watch`. Refines ADR-0023 (the cockpit composition is
now three master-detail browsers); consistent with ADR-0012 (no topic registry),
ADR-0016 (artifacts are the bus's), ADR-0017 (`artifact.list` joins the verb
surface), and ADR-0021 (self-enrolment). `artifact.list` is an additive verb — no
protocol-epoch bump.
