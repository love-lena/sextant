---
title: Migrate non-compliant top-level verbs into resource-verb form; rename conversation→chat
status: resolved
priority: P3
created_at: 2026-05-26T21:08-07:00
resolved_at: 2026-05-26T22:55-07:00
labels: [feature, cli, ergonomics, command-design]
discovered_in: triage pass post-CLI/TUI conventions adoption — convention says resource-verb but several top-level singletons violate it
---

## Resolution

Landed on branch `feat-cli-cobra-fang-resource-verb-001` together with
`[[feat-cli-cobra-fang-migration]]`. The cobra RootCmd tree was built
directly in resource-verb shape; legacy top-level verbs are preserved
as hidden aliases for one minor release with stderr deprecation notes
(suppressed under `--json`).

Migrations shipped:

- `sextant ask <agent> "<text>"` → `sextant agents chat <agent> "<text>"` (one-shot mode)
- `sextant conversation <agent>` → `sextant agents chat <agent>` (TUI mode)
- `sextant tail <subject>` → `sextant events tail <subject>` (new `events` noun)
- `sextant exec <agent> -- <cmd>` → `sextant agents exec <agent> -- <cmd>`
- `sextant start|stop|restart|status|logs` → `sextant daemon <verb>` (new `daemon` noun)

`sextant agents chat <agent> [text]` uses `cobra.RangeArgs(1, 2)`; when
`len(args) == 2` OR stdin is piped (not a TTY), one-shot mode is
selected. Otherwise the chat TUI launches.

Singletons `init`, `doctor`, `version` documented as explicit
exceptions in `conventions/tui-conventions.md` § "Command design".
`specs/cli/commands.md` and `docs/book/src/getting-started/first-run.md`
updated to the new tree.

## Summary

`conventions/tui-conventions.md` (Command design) pins:

> **Resource-verb-modifier, in that order.** `sextant agents list`,
> not `sextant list agents`. Verbs cluster under resources; matches
> the daemon shape and makes tab completion useful.

Several existing top-level verbs violate this and need to fold under a resource noun. Also: rename `conversation` → `chat` while we're touching that surface — shorter, matches `pkg/tui/chat/`, and is how operators actually say it.

## Migrations

| Today | Target | Notes |
|---|---|---|
| `sextant ask <agent> "<text>"` | `sextant agents chat <agent> "<text>"` | fold `ask` into `chat`: positional text → one-shot mode (prompt + wait + print, exit); no text → interactive TUI |
| `sextant conversation <agent>` | `sextant agents chat <agent>` | resource-verb + rename in one step |
| `sextant tail <subj>` | `sextant events tail <subj>` | new top-level noun `events` (NATS subjects, KV inspection) |
| `sextant exec <agent> -- …` | `sextant agents exec <agent> -- …` | exec into an agent's container |
| `sextant start` | `sextant daemon start` | new top-level noun `daemon` |
| `sextant stop` | `sextant daemon stop` | |
| `sextant restart` | `sextant daemon restart` | |
| `sextant status` | `sextant daemon status` | |
| `sextant logs` | `sextant daemon logs` | |

### One-shot vs interactive chat

Folding `ask` into `chat` collapses two verbs into one with mode determined by args:

- `sextant agents chat <agent>` — opens the chat TUI (current `conversation` behavior).
- `sextant agents chat <agent> "<text>"` — one-shot: send `<text>`, wait for `turn_ended`, print agent output to stdout, exit (current `ask` behavior).
- `sextant agents chat <agent> --json "<text>"` — one-shot, NDJSON envelope output.
- `echo "text" | sextant agents chat <agent>` — one-shot, prompt read from stdin (per the convention's stdin-as-fallback rule).

Cobra command: `cobra.Command{Use: "chat <agent> [text]", Args: cobra.RangeArgs(1, 2)}`. If `len(args) == 2` or stdin is piped → one-shot. Otherwise → TUI.

## Top-level exceptions (stay as-is)

`init`, `doctor`, `version` are verbs on the **sextant** resource itself — initialize the install, diagnose the install, show the install's version. Folding them under `sextant install <verb>` adds an explicit noun for a single-instance resource and buys nothing.

`conventions/tui-conventions.md` should be amended to name these explicit exceptions.

## Backwards compatibility

Cobra `Aliases` makes one-release deprecation cheap:

- Each migrated old form ships as an alias for one minor release.
- Emits a stderr deprecation note that names the new form (suppressed under `--json`).
- Example: `sextant ask foo "hi"` still works; stderr prints `note: 'sextant ask' is deprecated; use 'sextant agents chat'`. Stdout is byte-identical.
- Window: **one minor release.** Aliases removed in the release after. Project is pre-1.0 so a longer window isn't earned.

## Fix shape

1. **Land alongside** `[[feat-cli-cobra-fang-migration]]`. Cobra's `RootCmd` tree gets built once, in resource-verb shape, with aliases for the old forms. Doing it after cobra-fang is rework — every renamed command would migrate twice.

2. Introduce two new top-level nouns:
   - `daemon` — `start`, `stop`, `restart`, `status`, `logs`
   - `events` — `tail` (and future `events pub`/`events sub` debugging verbs against subjects + KV). Internal vocab (`pkg/sextantbus`) stays — `events` is the operator-facing name.

3. Update `specs/cli/commands.md` to reflect the new tree. Every old verb gets a note that points at its new home.

4. Update mdbook — `docs/book/src/getting-started/first-run.md` leads with `sextant daemon start` instead of `sextant start`.

5. Update `conventions/tui-conventions.md`:
   - Name `init`, `doctor`, `version` as the documented top-level exceptions (verbs on the sextant resource itself).
   - Cross-link this ticket as the rationale.

6. Sweep internal references: `cmd/sextant-tui-chat-preview/`, agent prompts/templates, any seed `CLAUDE.md`, README, getting-started chapters — anything that names `sextant conversation` or `sextant ask`/`tail`/etc. as top-level.

## Acceptance

- `sextant agents --help` lists `chat`, `exec` alongside existing `list`/`show`/`spawn`/`kill`/`restart`/`prompt`/`archive`.
- `sextant agents chat <agent>` opens the TUI.
- `sextant agents chat <agent> "ping"` round-trips one turn and exits 0; `--json` emits the envelope.
- `sextant daemon --help` lists `start`/`stop`/`restart`/`status`/`logs`.
- `sextant events --help` lists `tail`.
- `sextant conversation foo`, `sextant ask foo "..."`, `sextant tail subj`, `sextant start`/`stop`/`restart`/`status`/`logs`, `sextant exec …` all still work for one minor release: stderr prints a deprecation note naming the new path, stdout is byte-identical to the new form.
- `sextant init`, `sextant doctor`, `sextant version` unchanged.
- `conventions/tui-conventions.md` names the exception list.
- mdbook + `specs/cli/commands.md` reflect the new tree.

## Related

- `[[feat-cli-cobra-fang-migration]]` — land together; cobra's `RootCmd` tree is the migration vehicle.
- `[[feat-cli-verb-vocabulary-decision]]` — adjacent question about *which* verbs (`spawn`/`kill`/`prompt` vs `create`/`delete`/`update`). Independent but pairs naturally — decide both during the cobra-fang migration.
- `[[feat-cli-destructive-op-flags]]` — `daemon stop` is destructive; gets the `--dry-run`/`--yes`/Huh treatment.
- `conventions/tui-conventions.md` § "Command design → Resource-verb-modifier"
- `pkg/tui/chat/` — already named `chat`; this ticket aligns the CLI with the package, not the other way around.
