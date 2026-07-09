---
status: accepted
signed_off_by: lena
date: 2026-07-09
---

# Triage Labels

The engineering skills speak in five canonical triage roles. This file maps
them to the label strings used in our issue tracker (Linear, team `SX`).

| Role (mattpocock/skills) | Our label         | Meaning                                  |
| ------------------------ | ----------------- | ----------------------------------------- |
| `needs-triage`           | `needs-triage`    | Maintainer needs to evaluate this issue  |
| `needs-info`             | `needs-info`      | Waiting on reporter for more information |
| `ready-for-agent`        | `ready-for-agent` | Fully specified, ready for an AFK agent  |
| `ready-for-human`        | `ready-for-human` | Requires human implementation            |
| `wontfix`                | `wontfix`         | Will not be actioned                     |

When a skill mentions a role (e.g. "apply the AFK-ready label"), use the
corresponding string above.

## Applying labels in Linear

Labels are workspace-level; create once with `create_issue_label`, then
apply via `save_issue`'s `labels` field (replaces the full label set on that
issue — pass the union of what should remain, not just the addition).

Triage roles are orthogonal to workflow state (`Backlog`/`Todo`/`In
Progress`/`In Review`/`Done`/`Canceled`): an issue can be `Todo` +
`ready-for-agent`. Edit the right-hand column if you later adopt a
different vocabulary.
