---
status: accepted
signed_off_by: lena
date: 2026-06-02
---

# Conventions are optional, not core

Core is only what's *required*: the bus, the two primitives, the wire, and the
SDK. The clients registry, workflows, spawn, and request/reply are **not** core —
they aren't required to use Sextant, so they don't ship as part of it. They are
opinionated, Sextant-owned **conventions** built on the primitives, the same way
any client builds its own.

Each convention is a thin **contract** (addressing + a lexicon + rules) that
makes interop possible, plus — usually — a forkable **reference client** that
implements it (the Workflow contract → the coordinator; Spawn-request → the
Dispatcher). *Minimal contract vs. opinionated implementation* is the
convention-vs-client split.

Why not make them core / required (a framework)? Three reasons: (1) it bloats the
required surface and makes the conventions un-droppable; (2) "optional + forkable"
makes them *examples to copy*, not mandates — clients build their own conventions
the same way, which is the whole extensibility story; (3) it keeps the
multi-backend door open: a new backend implements the two primitives, and every
convention rides on top unchanged.

Map (ADR-0003): the "Sextant conventions" band, and its relationship to Clients
and the two primitives.
