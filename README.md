# sextant initial

The first considered implementation of sextant — a Go-based control plane for AI coding agents, built on NATS JetStream, ClickHouse, and Claude Code SDK sidecars.

> **Note on naming**: this is "initial" (v1). The earlier experimental version is "pilot" (v0), located in the `sextant` repo. Initial is a clean ground-up implementation informed by what pilot taught us — no code carryover from pilot, and the design has been considered top-to-bottom in [`specs/architecture.md`](specs/architecture.md).

## Install

```bash
git clone git@github.com:love-lena/sextant-initial.git
cd sextant-initial
make install            # NOT `cp bin/* ~/.local/bin/`; cp triggers macOS
                        # Gatekeeper SIGKILL (exit 137, silent kill).
                        # PREFIX overridable: `sudo make install PREFIX=/usr/local`
sextant init
sextantd &
```

> **macOS gotcha — do not use plain `cp`.** `cp bin/sextant ~/.local/bin/` stamps
> the `com.apple.provenance` xattr onto the destination, and Gatekeeper SIGKILLs
> the resulting binary on invocation (exit code 137, **no stderr message**). The
> failure looks like the binary itself is broken. `make install` invokes
> `/usr/bin/install` which writes a clean file, sidestepping the xattr entirely.
> Linux is unaffected. Cross-reference: `plans/issues/docs-install-via-make-install-not-cp.md`.

`make uninstall` removes every installed binary from `$PREFIX/bin`.

## What's in this repo

Right now: **specifications, plans, and conventions only**. No code yet. This repo is built in two phases.

### Phase 1: Classic Claude Code builds initial

Classic Claude Code (CLI mode) reads from this repo and implements the system following the specs. Linear, well-scoped work — no self-iteration needed during this phase.

Start point for any implementor (human or agent): [`plans/bootstrap.md`](plans/bootstrap.md).

### Phase 2: Sextant agents iterate on sextant

Once the system is functional enough to spawn its first agent, the **switchover** happens. After that, sextant agents work on sextant itself in parallel via git worktrees, with self-update, capability descoping, and audit trails all working.

## Directory layout

```
.
├── README.md                 # you are here
├── plans/
│   ├── bootstrap.md          # master plan: sequenced milestones
│   └── milestones/           # per-milestone detail (filled as we go)
├── specs/
│   ├── architecture.md       # the design pillars — load-bearing doc
│   ├── components/           # nats, clickhouse, sextantd, shipper, sidecar-image, libraries
│   ├── cli/                  # CLI verb shapes
│   └── protocols/            # bus subjects, RPC catalog, envelope schema
├── conventions/
│   ├── STYLE.md              # Go style (Uber baseline + sextant additions)
│   ├── git-workflow.md       # worktrees, branch naming, merge flow
│   └── tui-conventions.md    # keymap, layout, ui.state.* patterns
└── skills/                   # SKILL.md files for agents
    └── sextant-bootstrap-implementer.md
```

## Reading order for implementors

1. [`specs/architecture.md`](specs/architecture.md) — the why and the what of every load-bearing decision
2. [`plans/bootstrap.md`](plans/bootstrap.md) — the sequenced milestone list, top to bottom
3. [`conventions/STYLE.md`](conventions/STYLE.md) — how code is written here
4. [`conventions/git-workflow.md`](conventions/git-workflow.md) — how branches and merges work
5. Per-milestone spec files referenced from `bootstrap.md`

## Status

Pre-implementation. Plans and specs being filled in.
