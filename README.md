# Sextant

A protocol and an SDK for AI agents to communicate and collaborate over a bus.
The core is small and fixed — a bus, two primitives (Messages and Artifacts), a
wire format, and the SDK. Everything else is an optional, forkable convention or
a client you build.

> **Status: early.** The bus, CLI, dash, and Claude Code plugin run end to
> end; the API is still settling. Start with the
> [vision](docs/adr/0001-vision.md).

## Quickstart

Install the binaries with Homebrew (the repo is its own tap) and run the bus as
a managed daemon:

```bash
brew tap love-lena/sextant https://github.com/love-lena/sextant
brew install sextant            # sextant, sextant-mcp, sextant-dash (web), sextant-tui (terminal)
brew services start sextant     # the bus, now + on login (or `sextant up` to run it in the foreground)
```

Register an identity, then talk to the bus:

```bash
sextant clients register --self --name "$USER"   # mints creds, saves + activates a context
sextant publish msg.topic.hello '{"$type":"chat.message","text":"hello, bus"}'
sextant read msg.topic.hello
sextant-dash                                      # the web dash: serves the SPA on a 127.0.0.1 URL (THE dash)
sextant dash                                      # …open the running web dash in a browser (prints its URL)
sextant-tui                                       # the terminal UI: clients, topics, artifacts in a cockpit
```

Commands find the bus through a discovery file in the per-user store, so no URLs
or flags are needed once the service is running. To upgrade later, see
[Updating](#updating); `sextant --help` covers `--url`, `--store`, and contexts.

<details>
<summary>Without Homebrew</summary>

Build from a clone, or grab the prebuilt binaries from a release tarball:

```bash
go install ./clients/go/apps/{sextant,dash,tui,mcp}                  # from a clone (dash=web, tui=terminal)
# — or —
gh release download -R love-lena/sextant -p "*darwin_arm64*" -O - | tar -xz
install sextant_*/bin/* ~/.local/bin/                                 # anywhere on PATH
```

`darwin_arm64`, `darwin_amd64`, `linux_amd64`, `linux_arm64` are published;
`sextant version` prints the build. Run the bus yourself with `sextant up`.

</details>

To make a Claude Code session a bus client — the verbs as tools, inbound
messages pushed into the session:

```bash
claude plugin marketplace add love-lena/sextant     # the repo is the marketplace; @vX.Y.Z pins a release
claude plugin install sextant@sextant
```

The session gets its **own** bus identity automatically — minted per session,
reattached on resume, never your operator identity ([ADR-0029](docs/adr/0029-a-harness-speaks-as-itself.md));
no agent `register` step. Pin a specific identity with `SEXTANT_CONTEXT` in the
project's `.mcp.json` `env` if you need one.

Channel push is a Claude Code research preview behind an allowlist — start
sessions with `claude --dangerously-load-development-channels
plugin:sextant@sextant`; without the flag the tools still work and
`message_read` polling covers inbound.
[`clients/claude-code/`](clients/claude-code/README.md) has the rest: the
offline install from an unpacked tarball, per-project identities, and a demo.

## Updating

Updating has two halves — the **binaries** (Homebrew) and the **Claude Code
plugin** (skills, hooks, MCP wiring) — plus restarting the long-lived processes,
since none of them reload in place:

```bash
sextant update                  # brew update && brew upgrade love-lena/sextant/sextant
brew services restart sextant   # the running bus keeps the old binary until restarted
claude plugin marketplace update sextant && claude plugin update sextant@sextant
```

Then restart any active Claude Code sessions (each spawned its `sextant-mcp` at
startup and keeps using that process, so a session picks up the new server and
skills only on restart) and restart `sextant-dash` if it was up.
`sextant version` confirms the new build.
[`clients/claude-code/`](clients/claude-code/README.md#updating-to-a-new-version)
has the per-step detail.

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
