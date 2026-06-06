---
status: accepted
signed_off_by: lena
date: 2026-06-02
---

# Triage Labels

The engineering skills speak in five canonical triage roles. This file maps them
to the label strings used in our issue tracker (Backlog.md).

| Role (mattpocock/skills) | Our label         | Meaning                                  |
| ------------------------ | ----------------- | ---------------------------------------- |
| `needs-triage`           | `needs-triage`    | Maintainer needs to evaluate this issue  |
| `needs-info`             | `needs-info`      | Waiting on reporter for more information |
| `ready-for-agent`        | `ready-for-agent` | Fully specified, ready for an AFK agent  |
| `ready-for-human`        | `ready-for-human` | Requires human implementation            |
| `wontfix`                | `wontfix`         | Will not be actioned                     |

When a skill mentions a role (e.g. "apply the AFK-ready label"), use the
corresponding string above.

## Applying labels in Backlog.md
Labels are freeform strings applied via the CLI — never hand-edit the file:

    backlog task edit <id> --label needs-triage

Triage roles are **orthogonal to status** (`To Do` / `In Progress` / `Done`): a
task can be `To Do` + `ready-for-agent`. Edit the right-hand column if you later
adopt a different vocabulary.
