---
status: proposed
date: 2026-06-06
---

# A frozen surface lets the roadmap develop in parallel

This is a sequencing decision built on [ADR-0017](0017-the-verb-surface-is-the-protocol.md)
(the verb surface *is* the protocol), [ADR-0018](0018-the-bus-implements-the-protocol.md)
/ [ADR-0019](0019-implementing-the-bus.md) (the bus implements it), and
[ADR-0004](0004-conventions-are-optional.md) (conventions sit *over* the two
primitives, optional, not core). It records how the remaining roadmap — the dash,
the orchestration conventions (spawn, request/response, then the workflow
coordinator), cross-host reach, and the TypeScript SDK — develops as several
milestones built **in parallel** by separate agents, now that the rewrite has
landed on `main`, without their work colliding.

**Where we are.** The rewrite is on `main`: the cutover landed in **#91**, so
`main` now carries the whole protocol + SDK + the M2 MVP. The remaining milestones
are largely independent of one another. The risk in building them at once is not
the work; it is *collision* — independent agents editing the same core in parallel
produce merge conflicts and incoherent contracts. The whole decision below is
about making the work disjoint so parallelism is safe.

**The shared spine is named, and it is small.** The spine is the verb/operation
surface ([ADR-0017](0017-the-verb-surface-is-the-protocol.md)), the frame
([ADR-0006](0006-wire-atom.md)), the clients-registry record shape with its
connection-derived presence ([ADR-0020](0020-clients-are-bus-issued-identities.md)),
and connection/auth/creds. Everything else — every client, every convention, the
second SDK — is built *on* the spine and only *consumes* it. The dividing rule for
safe parallelism falls straight out of this: a track that only consumes the spine
and owns a disjoint set of packages is parallel-safe; a track that *changes* the
spine serializes.

**The surface `main` ships is the frozen contract.** Parallel work needs a stable
target, and `main` is it: the verb surface as shipped, pinned mechanically by the
conformance test (TASK-28), and the clients-registry/presence record shape as
settled. Freezing adds nothing new — it is the commitment to treat what shipped as
the contract every track builds against. Conventions are explicitly *not* spine:
request/response, spawn ([ADR-0009](0009-spawn.md)), and workflows
([ADR-0011](0011-workflows.md)) ride the two primitives and are built as ordinary
clients, so they do not move the surface the second SDK generates against.
request/response in particular is a convention that fans out with the M5a track,
not a verb added to the core.

**`main` is the line all new work branches from.** The cutover (#91) makes `main`
the protocol + SDK + MVP and the single base every track cuts from. The trees the
rewrite supersedes — the former daemon, container, and control-plane directories —
are not carried into `main`; the rewrite is the build going forward.

**Tracks fan out from `main`, each owning a disjoint set of packages.** Each
milestone is a worktree cut from `main` that consumes the frozen surface and writes
only its own packages: the dash in `pkg/tui/...` (its design pass is PR #87, the
dash-cockpit ADR — renumbering off 0021, now that 0021 is saved client contexts);
the orchestration conventions (the dispatcher + spawn + request/response) in their
own reference-client package; the M3 spike in docs plus a thin connection-config
seam; and the TypeScript SDK under `clients/typescript/`. Their package sets are
disjoint and the surface is frozen, so they neither block nor conflict with one
another. One worktree per track is the mechanical guard — concurrent agents never
share a working tree.

**Spine-changing work stays single-owner and serial.** Some later work *does*
change the spine: M3-proper (routable bind, TLS, creds distribution), creds
reissue, retention. The rule is **one writer per shared seam** — these run as their
own serial tracks behind the fan-out, never two at once on the same seam
(connection/auth, the frame, the registry shape). When such a change lands, the
fan-out tracks rebase onto it.

**The TypeScript SDK is highly parallelizable but not a priority.** Because it
consumes the frozen surface in its own package like any other client, its marginal
cost alongside the other tracks is low: it can be picked up whenever there is spare
capacity, and nothing else waits on it. So it stays *available* as a parallel track
rather than scheduled — low priority, not deferred. (The "abstract only against a
second implementation" discipline does not gate it either: it is a deliverable, not
a validation of the surface, and if a later spine change moves the surface it
rebases like the rest.)

**Consequences.** Several milestones progress at once, given a stable `main`
surface to build against and worktree isolation to keep concurrent agents off each
other's trees. Leftover pre-cutover PRs reconcile against `main`: the dash design
pass (#87) retargets to `main` and renumbers its ADR off 0021; the
root-`package.json` dependabot bump (#65) and the Backlog.md migration (#58) are
superseded by what `main` now carries and close. The roadmap
(`backlog/docs/doc-1`) updates to reflect this sequencing — parallel fan-out over a
frozen surface, with the spine-changers serial behind it — rather than the strict
M3 → M4 → M5 order.

Map ([ADR-0003](0003-high-level-architecture.md)): process and sequencing across
the bus, the SDK(s), and the clients.
