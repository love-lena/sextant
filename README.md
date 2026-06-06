# Sextant

A protocol and an SDK for AI agents to communicate and collaborate over a bus.
The core is small and fixed — a bus, two primitives (Messages and Artifacts), a
wire format, and the SDK. Everything else is an optional, forkable convention or
a client you build.

> **Status: greenfield rebuild in progress.** This branch carries the
> human-signed-off design canon; the implementation is being built against it.
> Start with the [vision](docs/adr/0001-vision.md).

## Where things are

- **Why we decided things** — [`docs/adr/`](docs/adr/) (the
  [index](docs/adr/README.md) lists the accepted decisions).
- **The shared language** — [`CONTEXT.md`](CONTEXT.md).
- **How to work here** — [`AGENTS.md`](AGENTS.md) (`CLAUDE.md` symlinks to it).
- **Human reference + API** — an mdbook under `docs/book/`, *forthcoming* (to be
  rewritten against the ADR-0018 architecture; the protocol source of truth lives
  in [`protocol/`](protocol/) and the [ADRs](docs/adr/) until then).
- **What's next** — tickets in [`backlog/`](backlog/) (Backlog.md).

## Agent skills

The engineering skills this repo uses
([mattpocock/skills](https://github.com/mattpocock/skills)) are committed under
`.claude/skills/`, so a fresh clone has them with no install.
[`skills-lock.json`](skills-lock.json) records their provenance.

## Optional: the Backlog.md CLI

Tickets live as plain markdown under `backlog/` and read fine as-is. To drive
them with the [Backlog.md](https://github.com/MrLesk/Backlog.md) board and CLI
— optional — install the pinned CLI once:

```bash
npm install --prefix tools/backlog
```

Then, for example:

```bash
tools/backlog/node_modules/.bin/backlog board          # the kanban board
tools/backlog/node_modules/.bin/backlog task list --plain
```

Reading tickets needs nothing; *writing* them should go through the CLI rather
than hand-editing the files (see
[`docs/agents/issue-tracker.md`](docs/agents/issue-tracker.md)).
