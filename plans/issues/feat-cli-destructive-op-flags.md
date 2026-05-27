---
title: Destructive CLI ops need --dry-run + --yes + Huh confirm on TTY
status: open
priority: P3
created_at: 2026-05-26T20:33-07:00
labels: [feature, cli, safety]
discovered_in: CLI/TUI conventions adoption
---

## Summary

`conventions/tui-conventions.md` (Tier 0 → Command design) pins:

> Destructive ops need `--dry-run` and confirmation. TTY: confirm
> via Huh. Non-TTY: require `--yes`. `--dry-run` prints what would
> happen, exits 0.

Current state:

- `sextant agents kill` (with or without `--archive`) takes the
  action immediately, no confirm.
- `sextant agents archive --all-dead` bulk-archives every
  lifecycle=defined agent in one shot — no confirm, no preview.
- `sextant agents restart` restarts immediately.
- `sextant stop` (daemon SIGTERM) and `sextant restart` take effect
  on press.
- Future verbs (`worktree destroy`, `self rollback`) are on the same
  path.

The mismatch between "this is destructive" and "no friction"
becomes a real problem the first time someone fat-fingers `kill`
against the wrong agent in production.

## Fix shape

1. Define a `cliflags.Destructive` helper that adds the standard
   trio to a Cobra command:
   - `--dry-run` — print what would happen, exit 0
   - `--yes` / `-y` — non-interactive confirmation
   - Behavior: on TTY without `--yes`, prompt via Huh; non-TTY
     without `--yes` fails with a structured error naming the flag.

2. Apply to:
   - `sextant agents kill <agent>` (incl. `--archive` variant)
   - `sextant agents archive <agent>` and `--all-dead`
   - `sextant agents restart <agent>`
   - `sextant stop` (with a stricter "are you sure?" because
     daemon-wide)
   - `sextant restart` (same)
   - Future: `sextant worktree destroy`, `sextant self rollback`,
     any `sextant test teardown`.

3. The `--dry-run` mode for `--all-dead` is especially valuable:
   list which agents would be archived, exit 0, no RPC issued.

4. Huh confirm text should name the resource and the action:

   ```
   ? Archive agent "smoke-1747" (a0b9...) — name will be released.
     Proceed? [y/N]
   ```

5. For `--all-dead`-style bulk ops, show the count and a few
   sample names before the confirm.

## Dependencies

Depends on [[feat-cli-cobra-fang-migration]] for Huh availability;
the destructive-flag helper sits on top of Cobra's flag groups.

## Acceptance

- `sextant agents kill <name>` on a TTY prompts via Huh, prints a
  named confirmation, and aborts cleanly on N.
- `sextant agents kill <name> --yes` proceeds without prompting.
- `sextant agents kill <name>` in a pipe (no TTY, no `--yes`)
  exits with a structured error: `kill requires --yes when stdin
  is not a TTY`.
- `sextant agents archive --all-dead --dry-run` lists targets,
  exits 0, no RPC sent.

## Related

- `conventions/tui-conventions.md` § "Command design — Destructive ops"
- [[feat-cli-cobra-fang-migration]]
