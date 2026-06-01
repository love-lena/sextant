# Issue tracker: Backlog.md

Issues, tasks, and PRDs for this repo live in **Backlog.md** — markdown files
under `backlog/`, driven by the `backlog` CLI. This is the source of truth,
**not** GitHub Issues (GitHub is used for pull requests only).

**The full workflow is documented in the `backlog` project skill**
(`.claude/skills/backlog/SKILL.md`): filing, driving, resolving, and finding
tickets, the P1–P3 priority ladder, what-to-file guidance, and the `[[slug]]`
cross-link convention. Read it before ticket work — this file only covers what
the engineering skills (`to-issues`, `to-prd`, `triage`) need to map their verbs
onto the `backlog` CLI.

## Ground rules

- **Never hand-edit ticket files.** Drive everything through the `backlog` CLI so
  the board, web UI, and on-disk markdown stay in sync.
- **Always pass `--plain`** for non-interactive (agent) output; the bare command
  opens a TUI.
- The CLI is the pinned binary at `tools/backlog/node_modules/.bin/backlog`
  (installed by `make backlog-install`). If `backlog` is on your PATH, that works too.

## Conventions

- **Slugs are the durable identity**, not numeric `task-N` IDs (IDs are reused
  after archiving). Cross-links use `[[slug]]`.
- **Resolve a slug to its task**: `rg "slug:<slug>" backlog/` — `backlog search`
  is fuzzy over titles/descriptions only and won't find slugs reliably.
- **Statuses** are `To Do` / `In Progress` / `Done` (orthogonal to triage labels —
  see `triage-labels.md`). Closed tickets move to `Done` and are archived off the
  live board.
- **Priority** uses `--priority high|medium|low` (the skill's P1/P2/P3 ladder).

## When a skill says "publish to the issue tracker"

Create a Backlog.md task:

    backlog task create "Title" -d "One-line description" \
      --ac "Acceptance criterion" --priority medium

## When a skill says "fetch the relevant ticket"

Resolve the slug, then view it:

    rg "slug:<slug>" backlog/          # find the task-N file
    backlog task <id> --plain          # view it

List open work with `backlog task list --plain`; the kanban board is `backlog board`.
