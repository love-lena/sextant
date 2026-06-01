# Triage Labels

The skills speak in terms of five canonical triage roles. This file maps those
roles to the actual label strings used in this repo's issue tracker (Backlog.md).

| Label in mattpocock/skills | Label in our tracker | Meaning                                  |
| -------------------------- | -------------------- | ---------------------------------------- |
| `needs-triage`             | `needs-triage`       | Maintainer needs to evaluate this issue  |
| `needs-info`               | `needs-info`         | Waiting on reporter for more information |
| `ready-for-agent`          | `ready-for-agent`    | Fully specified, ready for an AFK agent  |
| `ready-for-human`          | `ready-for-human`    | Requires human implementation            |
| `wontfix`                  | `wontfix`            | Will not be actioned                     |

When a skill mentions a role (e.g. "apply the AFK-ready triage label"), use the
corresponding label string from this table.

## Applying labels in Backlog.md

Labels are freeform strings applied via the CLI — never hand-edit the file:

    backlog task edit <id> --label needs-triage

These triage-role labels are **orthogonal to status** (`To Do` / `In Progress` /
`Done`): a task can be `To Do` + `ready-for-agent`. `wontfix` already matches
sextant's convention from the ticket migration — a won't-do ticket moves to
`Done`, carries the `wontfix` label, and is archived.

Edit the right-hand column if you later adopt a different vocabulary.
