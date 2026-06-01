# sextant

A Go control plane for AI coding agents. Sextant supervises a NATS JetStream bus, a ClickHouse store, and one Docker container per running agent — each container drives the Claude Agent SDK and reports back over the bus.

## Quickstart (operators)

```bash
git clone git@github.com:love-lena/sextant.git
cd sextant
make bootstrap                   # installs host deps, builds, installs, runs `sextant init`
sextant daemon start             # bring up the daemon
sextant agents create assistant --template default
sextant agents chat assistant
```

`make bootstrap` audits host deps (Go ≥ 1.26, `nats-server`, `clickhouse`, `docker`/OrbStack, `node`), prints what it's about to brew-install, prompts `Y/n`, then chains `make install` → `sextant doctor --preflight` → `sextant init`. Pass `YES=1` for non-interactive (CI / repeat runs).

macOS via Homebrew is the tested path. Linux is partial — `nats-server` and `clickhouse` aren't in default apt repos, so on Linux the script bails with upstream URLs if those are missing.

For a deeper walkthrough — what each step writes, how the daemon is supervised, where logs land — read [`docs/book/src/getting-started/first-run.md`](docs/book/src/getting-started/first-run.md).

### Verifying

```bash
sextant doctor
```

Runs ~15 checks: config files present, CA keypair valid, sextantd reachable, NATS and ClickHouse running, host binaries on PATH, installed binary's `GitSHA` matches the repo. Exit code `0` on green, `2` if anything failed. `sextant doctor --preflight` runs only the host-binary checks (faster, doesn't need the daemon running).

> **macOS gotcha — do not use plain `cp`.** `cp bin/sextant ~/.local/bin/` stamps `com.apple.provenance` onto the destination, and Gatekeeper SIGKILLs the resulting binary on invocation (exit 137, **no stderr**). The failure looks like the binary itself is broken. `make install` (which `make bootstrap` uses) invokes `/usr/bin/install`, which writes a clean file. Cross-reference: the `docs-install-via-make-install-not-cp` ticket in [`backlog/`](backlog/).

### Where to go next

The reference book lives in [`docs/book/`](docs/book/). Run `mdbook serve docs/book` to browse in a browser, or open the `.md` files directly.

- [CLI reference](docs/book/src/operator-guide/cli.md) — every `sextant <subcommand>`
- [TUIs](docs/book/src/operator-guide/tuis.md) — `sextant agents chat`, `sextant-tui-agents`
- [Templates](docs/book/src/operator-guide/templates.md) — defining new agent kinds
- [Worktrees](docs/book/src/operator-guide/worktrees.md) — how agents work in isolated git worktrees
- [Architecture overview](docs/book/src/architecture/overview.md) — the why behind the design

## Contributing

If you're working *on* sextant rather than driving it:

- **[`PRINCIPLES.md`](PRINCIPLES.md)** — three load-bearing values that constrain every feature decision. Read once.
- **[`CLAUDE.md`](CLAUDE.md)** — auto-loaded project guidance for AI agents (and a useful orientation for humans too).
- **[`conventions/`](conventions/)** — Go style, git workflow, TUI patterns, operator-experience conventions.
- **[`plans/bootstrap.md`](plans/bootstrap.md)** — the M0–M17 milestone plan. M0–M15 are merged on `main`; M16 (self-update) and M17 (test environments) are not implemented.
- **[`backlog/`](backlog/)** — open + closed bugs and feature requests, managed with the [Backlog.md](https://github.com/MrLesk/Backlog.md) CLI (`backlog task list --plain`); see the `backlog` skill.

For the build/test/lint loop, after `make bootstrap`:

```bash
make test       # go test -race + TS vitest + sidecar tests
make lint       # golangci-lint + nilaway + tsc --noEmit
make install    # rebuild and reinstall binaries to ~/.local/bin
```

Worktree-based feature work uses the `EnterWorktree` tool (Claude Code) or `git worktree add` directly — see [`conventions/git-workflow.md`](conventions/git-workflow.md).

Every commit carries `Co-Authored-By: <model> <noreply@anthropic.com>` when an AI participated. If you spawn subagents to commit, tell them to include the trailer too.

---

The earlier experimental Rust version, code-named "pilot" (v0), lives archived at [`love-lena/sextant-pilot`](https://github.com/love-lena/sextant-pilot). No code carryover; design reconsidered top-to-bottom for v1.
