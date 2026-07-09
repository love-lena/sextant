---
status: accepted
signed_off_by: lena
date: 2026-07-09
---

# Issue tracker: Linear

Issues live in **Linear** — team **Sextant** (key `SX`), workspace
`lena-sextant` (https://linear.app/lena-sextant/team/SX/overview). Not
GitHub Issues (GitHub is for pull requests only). Replaces Backlog.md,
retired 2026-07-09 with no migration — this is a clean start, not a
continuation.

## Conventions

- **Tools**: the `linear` MCP plugin (`mcp__plugin_linear_linear__*`) —
  no CLI, no hand-authored files.
- **Create an issue**: `save_issue` with `team: "SX"` + `title` (no `id`).
- **Read / list**: `get_issue` / `list_issues` (filter by `team`, `state`,
  `query`, etc.).
- **Comment**: `save_comment`.
- **Labels**: `list_issue_labels` / `create_issue_label`; apply via
  `save_issue`'s `labels` (replaces the full set — pass the union, not a diff).
- **Close**: `save_issue` with `state` set to `Done` or `Canceled` (this
  tracker has no hard delete — cancel rather than try to erase).
- Identifiers are `SX-<n>`; Linear generates a `gitBranchName` per issue
  (`linear/sx-<n>-title-slug`) — reasonable to reuse for the worktree branch
  when a ticket drives a tracked change.

## Pull requests as a triage surface

**PRs as a request surface: no.** GitHub PRs are not read into triage.

## When a skill says "publish to the issue tracker"

Create a Linear issue (`save_issue`, team `SX`).

## When a skill says "fetch the relevant ticket"

`get_issue` by `SX-<n>`.

## Wayfinding operations

Used by `/wayfinder`. The **map** is a single issue with **child** issues
as tickets.

- **Map**: a single issue labelled `wayfinder:map`, holding the Notes /
  Decisions-so-far / Fog body. `save_issue` with `team: "SX"`, `labels:
  ["wayfinder:map"]`.
- **Child ticket**: `save_issue` with `parentId` set to the map's issue —
  Linear's native sub-issues, visible in the map's UI. Labels:
  `wayfinder:<type>` (`research`/`prototype`/`grilling`/`task`). Once
  claimed, `assignee` is set to the driving dev.
- **Blocking**: Linear's **native issue relations** — `save_issue`'s
  `blockedBy` (append-only; pass the blocker's `SX-<n>`). Renders in
  Linear's UI as a visible blocked/blocking chip on both issues — the live
  gate. A ticket is unblocked when every blocker is `Done` or `Canceled`.
- **Frontier query**: `list_issues` scoped to the map's children (filter by
  `parentId` or just read the map's sub-issue list), drop any that are not
  `state: Todo`/`Backlog`, have an open blocker, or already have an
  `assignee`.
- **Claim**: `save_issue` with `assignee: "me"` — the session's first write.
- **Resolve**: `save_comment` with the answer, then `save_issue` with
  `state: "Done"`, then append a context pointer (gist + link) to the map's
  Decisions-so-far.
