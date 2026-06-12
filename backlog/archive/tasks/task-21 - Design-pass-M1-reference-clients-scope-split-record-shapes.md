---
id: TASK-21
title: 'Design pass: M1 reference clients (scope, split, record shapes)'
status: Done
assignee: []
created_date: '2026-06-04 05:43'
updated_date: '2026-06-12 17:47'
labels: []
milestone: 'M4: The dash (human UI)'
dependencies: []
references:
  - docs/adr/0009-spawn.md
  - docs/adr/0011-workflows.md
  - docs/adr/0014-the-tui-is-a-client.md
priority: medium
ordinal: 6500
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Design the M1 reference clients before building them, and decide how TASK-7 splits. TASK-7 names three forkable clients (human-messaging UI / dash, spawn Dispatcher, sextant.workflow/v1 coordinator), each mapping to a large ADR end-state. This task settles, for M1: (1) depth per client — minimal end-to-end exercise vs richer surface; (2) the split — almost certainly one ticket per client, with TASK-7 becoming an umbrella or being replaced; (3) the concrete record shapes the minimal versions need (spawn-request fields incl. lineage job/parent per ADR-0009; workflow Layer-0 state envelope + sextant.workflow/v1 fields per ADR-0011); (4) where clients live and how they run (standalone go run vs a launcher). Output: a short design/spec doc + the refined tickets. A dash-TUI prototype is being built first to iterate the human surface against Lena's eye and feed this design.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Per-client M1 depth decided and written down (minimal-vs-rich, with rationale)
- [x] #2 TASK-7 split decision made and the per-client tickets created/linked
- [ ] #3 Minimal record shapes specified: spawn-request (with lineage) and workflow Layer-0 state envelope
- [x] #4 Run/layout decision: where reference clients live and how they are launched in M1
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Prototype input (proto/dash-tui branch, throwaway): a runnable dash-TUI mockup iterated with Lena. Verdict that feeds this design:

- The dash is a COMPOSABLE PANE LIBRARY (ADR-0014): ship sensible defaults but make swapping/arranging panes first-class (btop-style customization). Don't pick one layout — the prototyped variants become built-in, forkable options.
- Cockpit is the default assembled layout (quadrant of pane-surfaces). Detail-on-demand: the detail pane is hidden by default and toggled, not always-on.
- Workflow pane: Checklist / Timeline / Pipeline are all keepers — selectable renderers of one workflow surface, each drillable into a step (owner, timing, produced artifact, events, control verbs).
- Artifact review card is a wanted pane: glamour-rendered markdown (the glow library); a Review variant adds inline comment markers anchored in the text + a comments panel. CAS/rev shown in the header.
- Role tokens (one hue per role) + status-by-shape glyphs read well; light + dark palettes both viable (prototype shipped both).

Design-pass TODO: turn these into the per-client split (the human-UI/dash likely its own ticket), the pane/widget library boundary (widgets ⊂ surfaces ⊂ dash, per ADR-0014), the layout-customization mechanism, and the minimal record shapes (workflow Layer-0 envelope per ADR-0011; spawn-request per ADR-0009).

Split DECIDED by Lena (2026-06-04): dash → M2 (TASK-7); Dispatcher → M4 (TASK-25); Workflow coordinator → M4 (TASK-26). MVP is manual-comms only (no spawn, no formal workflows). Remaining M2 scope of this design pass: the dash design (widget→surface→dash boundary per ADR-0014; the layout-customization mechanism; the pane variants as options) and the MVP record shapes (see TASK-12: a chat message kind + the artifact shape; spawn/workflow shapes defer to M4).
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Dash-design scope complete. Output: ADR-0023 (the dash is a composable pane cockpit; accepted, signed off 2026-06-05) + the TASK-7 umbrella fanned into build subtasks 7.1 theme+widget, 7.2 SDK-to-tea adapter, 7.3 surfaces, 7.4 layout engine, 7.5 the dash binary. Decisions: M4 panes = presence + message-stream + artifact (workflow -> M5); one read-stream + optional compose with round-trip merge; btop-faithful customization (presets + toggle + reflow + config); cmd/sextant-dash + thin 'sextant dash' alias. MVP record shapes already present (chat.message, document, client; protocol/methods.json). AC#3 (spawn-request + workflow Layer-0 shapes) reassigned to the M5 reference clients TASK-25/26.
<!-- SECTION:FINAL_SUMMARY:END -->
