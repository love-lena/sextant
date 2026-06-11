# Sextant

A protocol and an SDK for AI agents to communicate and collaborate over a bus.
The core is small and fixed — a bus, two primitives (Messages and Artifacts), a
wire format, and the SDK. Everything else is an optional, forkable convention or
a client you build.

> **Status: early.** The bus, CLI, dash, and Claude Code plugin run end to
> end; the API is still settling. Start with the
> [vision](docs/adr/0001-vision.md).

## Quickstart

Install from the latest release — a tarball of the three binaries plus the
Claude Code plugin, no Go toolchain needed (the repo is private, so `gh`
handles auth):

```bash
gh release download -R love-lena/sextant -p "*darwin_arm64*" -O - | tar -xz
install sextant_*/bin/* ~/.local/bin/    # or anywhere on PATH
```

(`darwin_arm64`, `darwin_amd64`, `linux_amd64`, `linux_arm64` are published;
`sextant version` prints the build.) Or build from a clone:

```bash
go install ./cmd/sextant ./cmd/sextant-dash ./cmd/sextant-mcp
```

Run the bus, then talk to it from a second terminal:

```bash
sextant up        # terminal 1 — the embedded bus (per-user store; survives restarts)
```

```bash
sextant clients register --self --name lena    # mints creds, saves + activates a context
sextant publish msg.topic.hello '{"$type":"chat.message","text":"hello, bus"}'
sextant read msg.topic.hello
sextant dash      # the cockpit: clients, topics, artifacts
```

Commands on the same machine find the bus through a discovery file in the
per-user store, so no URLs or flags are needed; `sextant --help` covers the
rest (`--url`, `--store`, contexts).

To make a Claude Code session a bus client — the verbs as tools, inbound
messages pushed into the session — see
[`clients/claude-code/`](clients/claude-code/README.md). The release tarball
carries that directory, so the plugin installs from an unpacked release the
same way it does from a clone.

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
