# Tickets moved to Backlog.md

This directory used to hold sextant's bug + feature tickets, one markdown
file per issue. **They now live in [`backlog/`](../../backlog/)** and are
managed with the [Backlog.md](https://github.com/MrLesk/Backlog.md) CLI.

The 94 tickets that lived here were migrated on 2026-06 (history preserved in
git). The design + field mapping is documented in
[`docs/superpowers/specs/2026-06-01-backlog-md-migration-design.md`](../../docs/superpowers/specs/2026-06-01-backlog-md-migration-design.md).

## Finding an old ticket

Each ticket kept its original slug as a `slug:` label, so a reference like
`plans/issues/feat-doctor-stale-binary-detection.md` (or a `[[slug]]`
cross-link) still resolves:

```sh
rg -l "slug:feat-doctor-stale-binary-detection" backlog/tasks backlog/archive
```

The matching file is named `task-<N> - ….md`. Closed tickets live under
`backlog/archive/tasks/`; open ones under `backlog/tasks/`.

## Working with tickets

Don't hand-edit ticket files — drive them through the `backlog` CLI. The
`backlog` skill (`.claude/skills/backlog/SKILL.md`) is the how-to: filing,
driving, resolving, and navigating, plus the P1–P3 priority ladder,
what-to-file guidance, and the `[[slug]]` cross-link convention.

```sh
backlog task list --plain     # open work
backlog board                 # kanban view (operator TUI)
backlog browser               # web UI
```
