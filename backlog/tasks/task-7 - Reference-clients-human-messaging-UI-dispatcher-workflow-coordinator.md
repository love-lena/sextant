---
id: TASK-7
title: 'Reference client: the human-messaging dash'
status: To Do
assignee: []
created_date: '2026-06-03 01:12'
updated_date: '2026-06-04 18:11'
labels: []
milestone: 'M4: The dash (human UI)'
dependencies:
  - TASK-3
  - TASK-6
  - TASK-21
references:
  - docs/adr/0023-the-dash-is-a-composable-pane-cockpit.md
  - docs/adr/0014-the-tui-is-a-client.md
priority: medium
ordinal: 7000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Umbrella for the M4 dash build — the forkable reference human-UI client
(ADR-0014): a composable pane cockpit over the SDK. The design was settled in the
TASK-21 pass; the customization mechanism (presets + toggle + reflow + config) and
the widget → surface → dash contract are **ADR-0023**. Fans out into subtasks:
TASK-7.1 theme + widget toolkit · 7.2 SDK→tea adapter · 7.3 surfaces (presence ·
message-stream · artifact) · 7.4 layout engine · 7.5 the dash binary. M4 panes =
presence + message-stream + artifact; the workflow pane and the reference
Dispatcher (TASK-25) + Workflow coordinator (TASK-26) are M5. MVP is manual-comms.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 `cmd/sextant-dash` (+ thin `sextant dash` alias) launches under a bus identity and assembles presence + message-stream + artifact into a cockpit default
- [ ] #2 composable pane library: built-in presets + per-pane toggle + reflow + persisted config (btop-style); detail-on-demand (hidden, toggled)
- [ ] #3 the in-tree library holds the widget ⊂ surface ⊂ dash strata; widgets/surfaces import only theme/SDK, never NATS or `internal/` (import checks pass)
- [ ] #4 e2e (**the goal**): launch → see presence + a live message stream → send a message (round-trip, no optimistic echo) → open an artifact; VHS demo + PTY verify
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
**Ownership model.** One agent owns this whole goal (the dash) and dispatches its own
subagents per subtask — not five independent hand-offs. Each subtask ticket (7.1–7.5)
carries a self-contained brief in its own Implementation Notes; the owner reads them and
farms out the work, then integrates.

**Build order / critical path:** `(7.1 ∥ 7.2) → 7.3 → 7.4 → 7.5`. 7.1 (theme+widgets,
no SDK) and 7.2 (SDK→tea adapter; deps TASK-3/4, both shipped in M2) start in parallel
immediately; 7.3 surfaces need both; 7.4 layout needs 7.3; 7.5 binary needs 7.4 and
proves AC#4 above.

**Scope.** M4 panes = presence + message-stream + artifact. The workflow pane + the
reference Dispatcher (TASK-25) and Workflow coordinator (TASK-26) are M5; MVP is
manual-comms. Design is ADR-0023 (sharpens ADR-0014). `proto/dash-tui` sets the look;
7.1 absorbs then deletes it.

**Process.** One module per worktree off `main` (ADR-0022); isolate each subagent's
worktree to avoid cross-writes; `gofumpt` before push; PRs into `main`.
<!-- SECTION:NOTES:END -->
