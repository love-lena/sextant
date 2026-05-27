---
title: Decide whether existing domain verbs fold into list/show/create/update/delete/run
status: open
priority: P3
created_at: 2026-05-26T20:33-07:00
labels: [feature, cli, needs-input, command-design]
discovered_in: CLI/TUI conventions adoption
---

## Summary

`conventions/tui-conventions.md` (Tier 0 → Command design) pins a
fixed verb vocabulary:

> **Fixed verb vocabulary.** `list`, `show`, `create`, `update`,
> `delete`, `run`. Custom verbs (`approve`, `escalate`) live as
> flags or subcommands of `update`, not top-level.

Sextant already ships meaningful domain verbs that don't slot into
that vocabulary. The question is whether to migrate them, relax the
convention, or carve out a documented exception.

This ticket exists to track the decision, not to prescribe an
implementation.

## Existing domain verbs

| Verb | Resource | Current call |
|---|---|---|
| `spawn` | `agents` | `sextant agents spawn <name> --template T` |
| `kill` | `agents` | `sextant agents kill <agent>` |
| `restart` | `agents` | `sextant agents restart <agent>` |
| `archive` | `agents` | `sextant agents archive <agent>` |
| `prompt` | `agents` | `sextant agents prompt <agent> "<text>"` |
| `answer` | `pending` | `sextant pending answer <id> "<text>"` |
| `defer` | `pending` | `sextant pending defer <id>` |
| `escalate` | `pending` | `sextant pending escalate <id> --to <agent>` |
| `query` | `audit` | `sextant audit query` |
| `tail` | `audit` / top-level | `sextant audit tail` / `sextant tail <subj>` |
| `create` / `destroy` / `merge` / `diff` | `worktree` | spec'd, partly built |

## Options

### Option A — Migrate to fixed vocabulary

Fold every domain verb under `update`:

```
sextant agents spawn foo --template T
  → sextant agents create foo --template T

sextant agents kill foo
  → sextant agents delete foo
    OR sextant agents update foo --lifecycle=ended

sextant agents archive foo
  → sextant agents update foo --lifecycle=archived

sextant agents prompt foo "hi"
  → sextant agents update foo --prompt "hi"
    OR introduce a new noun: sextant prompts create --agent foo --text "hi"

sextant pending answer <id> "..."
  → sextant pending update <id> --answer "..."

sextant pending escalate <id> --to bar
  → sextant pending update <id> --escalate-to bar
```

Pros:
- Strict adherence to convention; matches typical REST CRUD shape.
- New verbs land via flags, not new top-level names.

Cons:
- `spawn`/`kill`/`prompt`/`answer` are operator-clear in a way
  `update --kind=prompt` is not. The cognitive cost is real.
- Existing muscle memory, docs, and `specs/cli/commands.md`
  bake in the current verbs.
- `update --lifecycle=ended` loses the affordance that killing
  also tears down the container, releases volumes, etc.

### Option B — Relax the convention

Amend the conventions doc to say:

> Default to CRUD verbs (`list`, `show`, `create`, `update`,
> `delete`, `run`). Domain verbs are allowed when they communicate
> operator intent better than `update --kind=X` — `spawn` /
> `kill` / `archive` / `prompt` / `answer` / `escalate` are kept
> for that reason. The bar: a domain verb has to map to a
> first-class operator concept that wouldn't be visible if
> collapsed into `update`.

Pros:
- Keeps the existing surface and the operator clarity it has.
- Doesn't churn `specs/cli/commands.md` or the docs site.
- Still constrains new verbs to a justification.

Cons:
- "Operator-clear" is subjective; the convention loses some teeth.
- Risks creep — every new feature ticket can argue for a new
  top-level verb.

### Option C — Hybrid (carve exceptions list)

Same as Option B but explicitly enumerate the allowed exceptions
in the conventions doc and require new exceptions to go through a
needs-input ticket like this one.

## Needs input

Lena's call. Lean recommendation from this ticket's author: Option
C — keep the existing operator-clear verbs as a closed exception
list, require justification for adding more. Tracks the spirit of
"fixed vocabulary" without the cost of a verb-migration that
breaks every script and doc the project has.

## Acceptance (once decided)

- Conventions doc updated to reflect the decision.
- If A: a migration ticket per resource (agents, pending, worktree)
  filed; `specs/cli/commands.md` updated; backwards-compatible
  aliases for the old verbs across at least one release.
- If B or C: conventions doc amended; this ticket closed as
  resolved.

## Related

- `conventions/tui-conventions.md` § "Tier 0 → Command design →
  Fixed verb vocabulary"
- `specs/cli/commands.md` (every section)
- [[feat-cli-cobra-fang-migration]] — verb migrations land
  cleanest during framework swap
