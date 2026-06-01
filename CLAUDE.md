# Working in sextant

This file is auto-loaded into every Claude Code session. Skim it before
starting work; follow the links when the matching domain comes up.

## Read before deciding anything

- **`PRINCIPLES.md`** — three load-bearing values that constrain every
  feature decision. Read once per session.

## Versioning + PR policy

- **Every change lands via a pull request.** No direct commits to
  `main` — not for code, not for docs, not for spec/plan notes.
  Self-approved fast-merges are fine; bypassing the PR path is not.
  See `conventions/git-workflow.md` for the workflow.
- **Never use `gh pr merge --admin`** or the web UI's "Merge
  without waiting for requirements" button. Branch protection on
  `main` is configured with `enforce_admins: true` so every merge
  — including the repo owner's — must pass the required CI checks
  (`lint + test (Go)`, `lint + test (TypeScript)`, `sidecar image
  build + smoke`, `changelog entry required`). The `--admin`
  override is a manual escape hatch the human owner uses
  deliberately in genuine emergencies, NOT a thing Claude reaches
  for. If CI fails: fix the underlying issue and push a new
  commit, don't bypass.
- **Feature PRs do NOT touch `VERSION`. Releases are cut, not
  accrued.** A normal PR adds its `CHANGELOG.md` entry under
  `## [Unreleased]` and stops there. The `VERSION` bump + tag
  happen later on a dedicated `release: cut vX.Y.Z` PR. Per-PR
  bumps just guarantee merge conflicts on the most-contended
  one-line file in the repo; `[Unreleased]` is what decouples
  "change described" from "version cut." Full rationale +
  workflow: **`conventions/versioning.md`**.
- **The bump is read off the changelog, not chosen by feel.** At
  cut time: any `Removed` (or behavior-breaking `Changed`) →
  **MAJOR**; else any `Added` → **MINOR**; else (only `Fixed` /
  non-breaking `Changed`) → **PATCH**. Classification test is
  *observability, not diff size*: does someone who only runs the
  binary notice, and does it break a working invocation? A 2k-line
  internal refactor with no observable change is PATCH-or-nothing;
  a one-char default-flag change is MAJOR. Ambiguity → larger bump.
- **Changelog entry IS required per PR** (this is unchanged and
  CI-gated). `CHANGELOG.md` follows
  [Keep a Changelog](https://keepachangelog.com); add to the right
  `## [Unreleased]` subsection (Added / Changed / Deprecated /
  Removed / Fixed / Security). A PR touching a **shipping path**
  without a `CHANGELOG.md` edit fails the `changelog entry
  required` check.
  - **Shipping paths** (entry required): `cmd/**`, `pkg/**`
    (non-test), `images/**`, `clients/**`, `Makefile`, `go.mod`,
    `go.sum`, `pkg/sextantproto/schemas/**`, `VERSION`.
  - **Metadata paths** (no entry needed): `docs/**`, `plans/**`,
    `conventions/**`, `.github/**`, `.claude/**`,
    `tests/visual/**`, root `*.md`, and pure `*_test.go` changes.
- **There are four version surfaces; don't assume they move
  together.** `VERSION` → `pkg/version.Version` is the **operator
  contract** (the source of truth, plumbed via `-ldflags`, shown by
  `sextant version`). `sextantproto.ProtoVersion` + the TS
  `PROTO_VERSION` are the **wire contract**. `clients/typescript`'s
  `package.json` is the **library contract**. The sidecar self-report
  is its own string. They are *currently* partially coupled and
  partially stale — the target model (each line bumped by its own
  contract's breakage) and the decoupling follow-up live in
  `conventions/versioning.md`.

## Helping someone onboard

If the user asks how to get started with sextant, how to install it,
or how to drive it for the first time, point them at:

- `README.md` — the one-page quickstart (operator path on top,
  contributor path below)
- `docs/book/src/getting-started/{install,first-run,repo-tour}.md`
  — the deeper walkthrough (run `mdbook serve docs/book` to browse
  in a browser, or open the `.md` files directly)

Don't reinvent install instructions inline. The mdbook is the source
of truth for the installed-and-running flow; the README is the source
of truth for the quickstart. `make bootstrap` is the canonical
first-command — if someone hits a problem with it, debug there
rather than routing around it.

## Build / run / install

- **`make install`** installs sextant to `~/.local/bin/sextant`. Do
  NOT use `go install` — it puts the binary at `~/go/bin/` where the
  operator's PATH won't pick it up.
- **`make test`** runs the full Go + TS + sidecar test matrix.
- **`make lint-go`** has ~26 pre-existing issues across the repo. When
  checking whether your work introduced any, filter to files you
  touched; the global count is separately tracked.

## Branch isolation

Use the `EnterWorktree` tool to create an isolated workspace under
`.claude/worktrees/feat-<X>-impl`. Don't fall back to `git worktree
add` manually — `EnterWorktree` is the project's native tool and
manages cleanup.

## Convention docs (read when the surface matches)

- `conventions/STYLE.md` — Go style + general code conventions
- `conventions/tui-conventions.md` — Bubble Tea / Lipgloss patterns
  and TUI/CLI design rules; read before any TUI work
- `conventions/operator-experience.md` — CLI ergonomics, diagnostic
  surface design, recurring failure-mode patterns
- `conventions/git-workflow.md` — branch, commit, PR conventions
- `conventions/versioning.md` — the four version surfaces, the
  observability-based bump test, and the release-cut workflow;
  read before bumping a version or cutting a release

## Tickets

Bug + feature tickets live in `backlog/` and are driven by the
[Backlog.md](https://github.com/MrLesk/Backlog.md) CLI — **never
hand-edit ticket files**, drive them through `backlog` so the board,
web UI, and on-disk markdown stay in sync. The `backlog` skill
(`.claude/skills/backlog/SKILL.md`) is the how-to: filing, driving,
resolving, and finding tickets, plus the P1–P3 priority ladder,
what-to-file guidance, and the `[[slug]]` cross-link convention.

- `backlog task list --plain` — open work · `backlog board` — kanban
- `rg "slug:<slug>" backlog/` — resolve a `[[slug]]` to its task

Migrated from the old `plans/issues/` markdown (2026-06); that
directory is now a tombstone redirect, and the migration is documented
in `docs/superpowers/specs/2026-06-01-backlog-md-migration-design.md`.

## Commit footers

Every commit on this project carries a
`Co-Authored-By: <model> <noreply@anthropic.com>` footer. If you
spawn subagents to make commits, tell them to include it — otherwise
the commit gets flagged for missing attribution.

## Memory

Personal-process notes for future-Claude live at
`/Users/lena/.claude/projects/-Users-lena-dev-sextant/memory/`.
Read `MEMORY.md` there for context this CLAUDE.md intentionally
doesn't capture (working-with-Lena patterns, debugging shortcuts,
subagent-process specifics).
