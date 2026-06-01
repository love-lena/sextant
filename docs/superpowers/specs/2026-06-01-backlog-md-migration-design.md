# Replace `plans/issues/` with Backlog.md — migration design

- **Date:** 2026-06-01
- **Status:** approved (brainstormed + validated against a live install)
- **Branch / PR:** `feat-backlog-md-migration` (separate from the skill PR #55)

## Goal

Replace sextant's hand-rolled `plans/issues/` markdown ticket system with
[Backlog.md](https://github.com/MrLesk/Backlog.md), migrating **all 94
existing tickets** (95 files − `README.md`) into it. Backlog.md is the same
idea — git-native, markdown-native tickets — but adds a CLI, a kanban TUI, and
a web UI on top.

### Decisions (locked during brainstorming)

1. **Replace & migrate** — Backlog.md becomes canonical; `plans/issues/` is
   retired (tombstone redirect, history preserved in git).
2. **All 94 tickets move, history preserved** — closed tickets import as `Done`
   and are archived (off the live board), so the files and the full 330-link
   cross-reference graph survive.
3. **Slugs stay the identity.** Backlog.md's numeric `task-N` IDs are unstable
   (reused after archiving — see Findings), so the original slug is the durable
   handle: carried as a `slug:<slug>` label, and `[[slug]]` links stay verbatim
   as prose.
4. **A skill, not the MCP** — agents drive the CLI via
   `.claude/skills/backlog/SKILL.md` (PR #55). No MCP server, no vendored
   CLAUDE.md.
5. **`autoCommit: false`** — ticket edits land through the normal PR flow, not
   tool-generated commits (which would miss the required co-author footer).

## Source → target

`plans/issues/*.md` frontmatter (documented in its `README.md`) maps onto
Backlog.md task fields as follows:

| `plans/issues` field | → Backlog.md |
|---|---|
| `title` | `title` |
| filename slug | `slug:<slug>` label (the stable identity) |
| `status: open` | status `To Do` |
| `status: in-progress` | status `In Progress` |
| `status: fixed` | status `Done` + label `closed:fixed` + archived |
| `status: resolved` | status `Done` + label `closed:resolved` + archived |
| `status: wontfix` | status `Done` + label `wontfix` + archived |
| `status: deferred` | status `To Do` + label `deferred` |
| `priority: P1/P2/P3` | `priority: high/medium/low` + label `P1/P2/P3` |
| `labels: [...]` | merged into `labels` |
| `created_at` | `created_date` (post-processed; the CLI stamps "now") |
| `discovered_in`, `fixed_in`, `fixed_at`, `resolved_at`, `resolved_by` | `## Implementation Notes` provenance block |
| `resolution` (block scalar) | `## Final Summary` |
| body (`## Summary`, `## Acceptance`, `## Related`, …) | `## Description`, **verbatim** |

### Body handling — losslessness over native-feature fidelity

The original body is dropped into the Description **whole**, preserving every
section and every `[[slug]]` link exactly. We deliberately do **not** try to
auto-split `## Acceptance` text into Backlog.md's native `--ac` checklist:
across 94 hand-written tickets the section shapes vary too much, and a bad
split silently corrupts content. Acceptance text survives as prose; promoting
individual tickets to native checkboxes is cheap follow-up work, ticket by
ticket. The P1–P3 tier, slug, and provenance are the parts that *must* be
machine-readable, and those become labels / notes.

### Cross-links

The 330 `[[slug]]` references stay verbatim as prose pointers — same semantics
as today. To resolve one back to a task, **ripgrep the files**
(`rg -l "slug:<slug>" backlog/tasks backlog/archive`); `backlog search` only
indexes titles/descriptions, not labels. This is documented in the skill.

## Config (`backlog/config.yml`)

Created with `backlog init "sextant" --integration-mode none` (no MCP, no
vendored agent instructions — we ship a skill instead). Then:

- `statuses: ["To Do", "In Progress", "Done"]` (defaults; no custom `Won't Do`
  — see Findings).
- `auto_commit: false`.
- `remote_operations: false` (avoids the "no remote configured" warning and
  cross-branch scanning noise during normal work).

## Install / bootstrap (Principle 1: one command)

Backlog.md is a Node tool. It is pinned in `tools/backlog/package.json`
(+ lockfile) and installed by a `make` target wired into `make bootstrap`, so
"rebuild and run" stays a single command and the version is reproducible. The
binary is invoked from `tools/backlog/node_modules/.bin/backlog`; `node_modules`
is gitignored.

## Migration tool

`tools/backlog-migrate/` — a Go one-shot (uses the repo's `gopkg.in/yaml.v3`)
that:

1. parses each `plans/issues/*.md` (frontmatter + body),
2. shells out to `backlog task create` with the mapped flags (exec args, no
   shell — arbitrary body content is safe),
3. rewrites each generated file's `created_date` to the original `created_at`,
4. after all 94 are created (unique IDs `task-1`..`task-94`), archives the 77
   closed ones,
5. emits a slug→id audit map, status/priority counts, and a dangling-link
   report.

Creating everything *before* archiving is what avoids Backlog.md's ID-reuse
behavior. It is committed for review/reproducibility, not wired into anything.

## Retire `plans/issues/` + docs

- `git rm` the 94 tickets; replace `plans/issues/README.md` with a tombstone
  redirect to `backlog/` + the skill.
- Rewrite `CLAUDE.md`'s "Tickets" section to point at the skill and `backlog/`.
- Sweep `conventions/**`, `docs/**`, root `*.md` for `plans/issues` references.

## Acceptance

- 94 tasks created; counts match the source: **17 active** (16 `open` + 1
  `deferred`) in `tasks/`, **77 archived** (46 `resolved` + 28 `fixed` + 3
  `wontfix`).
- Priority split preserved: 12 high (P1), 33 medium (P2), 49 low (P3).
- Every `[[slug]]` either resolves to an existing `slug:` label or is listed in
  the dangling-pointer report (links to never-filed slugs are expected and
  fine).
- `backlog task list --plain` and `backlog board` render without error.
- Canonical + user-facing references (CLAUDE.md, README.md, docs/book) point
  at `backlog/` + the skill. Historical breadcrumbs in Go comments / specs
  (`// See plans/issues/<slug>.md`) are left in place — the slug survives as a
  `slug:` label, so they stay resolvable via the tombstone's `rg` instructions
  — and listed in the PR for an optional follow-up sweep rather than rewritten
  across ~15 shipping-path files in this PR.

## Findings from the live install (`backlog.md@1.45.1`)

These shaped the design and the skill:

- `backlog search "slug:…"` returns nothing — search is fuzzy over
  titles/descriptions only, not labels → resolve slugs with `rg`.
- `Won't Do` is not a default status; `config set` refuses to edit statuses
  (file-edit only); Backlog.md mangles apostrophes in status names → `wontfix`
  uses `Done` + a `wontfix` label, no custom status.
- `board export --readme` overwrites the repo `README.md` → not used.
- Numeric IDs are reused after archiving → slugs are identity; create-all-then-
  archive.
- `init --integration-mode none` cannot be combined with `--agent-instructions`
  → use `--integration-mode none` alone.

## Not doing (YAGNI)

No MCP · no vendored CLAUDE.md · no auto-commit · no CI gate on backlog state ·
no cross-branch task loading · no auto-promotion of acceptance prose to native
checklists · no rewriting of historical ticket bodies beyond the mechanical
mapping.

## PR plan

Lands as a **separate PR** from the skill (#55), based on `main`. Because the
migration's `CLAUDE.md` edit points at `.claude/skills/backlog/`, **merge #55
first** (or together). A `CHANGELOG.md` `[Unreleased]` entry is required (the
PR touches `Makefile`, a shipping path).
