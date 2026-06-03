---
status: accepted
signed_off_by: lena
date: 2026-06-02
---

# Issue tracker: Backlog.md

Issues, tasks, and PRDs for this repo live in **Backlog.md** — markdown files
under `backlog/`, driven by the `backlog` CLI
([MrLesk/Backlog.md](https://github.com/MrLesk/Backlog.md)). This is the source
of truth, **not** GitHub Issues (GitHub is for pull requests only).

The full ticket workflow belongs in a `backlog` project skill (to be installed —
filing, driving, resolving, finding; the priority ladder; the `[[slug]]`
cross-link convention). This file covers only what the engineering skills
(`to-issues`, `to-prd`, `triage`) need to map their verbs onto the `backlog` CLI.

> **Setup pending:** the `backlog` CLI and the `backlog/` directory are not yet
> created in this repo. The conventions below are the intended target.

## Ground rules
- **Never hand-edit ticket files.** Drive everything through the `backlog` CLI so
  the board, web UI, and on-disk markdown stay in sync.
- **Always pass `--plain`** for non-interactive (agent) output; the bare command
  opens a TUI.

## Conventions
- **Slugs are the durable identity**, not numeric `task-N` IDs (IDs get reused
  after archiving). Cross-links use `[[slug]]`.
- **Resolve a slug**: `rg "slug:<slug>" backlog/` — `backlog search` is fuzzy
  over titles/descriptions only and won't find slugs reliably.
- **Statuses** are `To Do` / `In Progress` / `Done`, orthogonal to triage labels
  (see `triage-labels.md`).
- **Priority** uses `--priority high|medium|low`.

## When a skill says "publish to the issue tracker"

    backlog task create "Title" -d "One-line description" \
      --ac "Acceptance criterion" --priority medium

## When a skill says "fetch the relevant ticket"

    rg "slug:<slug>" backlog/     # find the task-N file
    backlog task <id> --plain     # view it

List open work with `backlog task list --plain`; the board is `backlog board`.

**Raw-file grep is a sanctioned backup for discovery** (read-only): `rg` directly
over the `backlog/` markdown often searches better than `backlog search`. It does
not violate "never hand-edit" — grep reads, it doesn't write.
