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
- **Semver is bumped per [semver.org](https://semver.org).** Breaking
  changes bump MAJOR, additive features bump MINOR, bug-only changes
  bump PATCH. Bump as part of the PR that introduces the change; do
  not batch bumps.
- The top-level `VERSION` file is the **source of truth** for the
  binary semver. `make install` / `make build` plumb it into
  `pkg/version.Version` via `-ldflags`. `sextant version` and
  `sextantd version` surface the value at runtime.
- `pkg/sextantproto/envelope.go::ProtoVersion` currently tracks the
  same number; if the wire format and the binary semver diverge,
  split them and file a follow-up for wire-format negotiation.

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

## Tickets

`plans/issues/` holds bug + feature tickets, one file per issue.
`plans/issues/README.md` documents the frontmatter schema, priority
ladder, cross-link syntax (`[[other-slug]]`), and what to file vs
just fix.

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
