---
id: TASK-186
title: Extract the web dash into a standalone sextant-dash binary (ADR-0046)
status: Done
assignee: []
created_date: '2026-06-23 19:33'
updated_date: '2026-06-24 01:01'
labels:
  - feature
  - dash
  - components
  - build
  - adr
  - 'slug:feat-dash-standalone-binary'
  - P1
  - ready-for-agent
dependencies: []
priority: high
ordinal: 176000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Today the web dash server is not its own binary: it rides inside sextant-dash (clients/go/apps/dash/), which is ALSO the terminal cockpit -- dash.Run branches on opts.Serve (internal/dash/dash.go:133) to pick TUI vs HTTP serve. That fusion is why the dash could not be a clean component (ADR-0040 kept it out of the Registry as 'the operator's foreground surface, not a keep-alive runtime', components.go:69). Decision (this session): the BROWSER dash is now THE dash, and the two surfaces split into two binaries. (1) A new standalone binary, sextant-dash, owns the web serve path (serve + mint), like sextant-violet/sextant-workflow. (2) The existing cockpit is renamed sextant-tui and REFRAMED from 'the dashboard' to a first-class CLI/TUI feature (NOT deprecated, NOT retired) -- but its --serve capability is stripped out, since it no longer serves anything; the HTTP/serve path lives only in sextant-dash. Foundational lift-and-shift slice: the new sextant-dash keeps the existing standing-connection behaviour for now; statelessness ([[feat-dash-stateless-mint-on-demand]]) and component registration ([[feat-dash-managed-component]]) build on top. Carries ADR-0046 (records the split; refines ADR-0044 by pinning the dash process connection LIFETIME; extends ADR-0040 so the dash joins the managed Registry) plus CONTEXT.md / mdbook updates. Shipping the binaries via brew is split out to [[feat-dash-release-packaging]].
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 New standalone binary named sextant-dash, a thin main.go in the violet/workflow mold, lives at clients/go/apps/dash (the dir scripts/release.sh + scripts/gen-formula.sh ALREADY map to sextant-dash, so the binary keeps its source dir); it owns the web serve path (serve + mint), backed by internal/dashapi (embedded web/app + favicon unchanged); --serve, runServe, serve.go and the dashapi HTTP serving live ONLY here
- [ ] #2 The former cockpit binary is renamed sextant-tui — its thin main.go moves to a new package clients/go/apps/tui — and reframed as a first-class CLI/TUI feature (NOT deprecated, NOT retired); --serve and the entire HTTP/serve path are stripped out of it (it no longer serves); internal/dash retains only the terminal-UI code (no dashapi import); the dash-layoutgallery/surfacegallery/widgetgallery preview binaries still build
- [ ] #3 pkg/tui/widget is untouched (other TUIs depend on it)
- [ ] #4 Both binaries build + run from a clean tree: `go build -o /tmp/sextant-dash ./clients/go/apps/dash` and `go build -o /tmp/sextant-tui ./clients/go/apps/tui` succeed and each launches; `make build` still builds bin/sextant; `go test ./clients/go/apps/internal/dash/... ./clients/go/apps/internal/dashapi/...` stays green. NOTE: there is no `make install` target — local install is out of scope, and release/brew packaging (the release.sh binary map + gen-formula.sh, which need the new sextant-tui entry + the dash-dir confirmation) is split to feat-dash-release-packaging
- [ ] #5 ADR-0046 is ALREADY accepted + merged (PR #247) — do NOT re-author it; this ticket IMPLEMENTS its decision and updates CONTEXT.md + mdbook + the `sextant`/`sextant-tui` --help/usage text + README (which still shows `sextant dash --serve`) to name sextant-dash (web, THE dash) vs sextant-tui (terminal UI); docgen clean
- [ ] #6 The `sextant dash` alias (cmdDash) and `sextant dash url` RESOLVE/OPEN the running web dash URL — they no longer SERVE (cmdDash's --serve/dash.Run-serve path is removed; serving is only the sextant-dash binary per AC#1); the terminal UI is reached via sextant-tui, not via `sextant dash`
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1) New package + thin main.go for sextant-dash; MOVE runServe/serve.go + serve-side identity + the dashapi HTTP serving into it (backed by internal/dashapi; embedded web/app + favicon unchanged). 2) Rename the cockpit cmd dir + Command doc to sextant-tui; STRIP --serve and the serve path out of it so internal/dash holds only the terminal UI. 3) Wire both binaries into the Makefile (local build/install). 4) Write ADR-0046 + CONTEXT/mdbook + run docgen. Lift-and-shift only: do NOT change mint/connection behaviour here; leave release.yml + Homebrew to [[feat-dash-release-packaging]].
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: design session 2026-06-23. ADR-0046 was accepted + merged in PR #247 (this work), so the build is unblocked AFK and the implementer does NOT write the ADR. Refines ADR-0044 (browser dash is a direct ws client); extends ADR-0040 (agent runtimes run as OS-managed components). TUI fate decided 2026-06-23: keep as a non-deprecated terminal feature, strip --serve (recorded in [[decision-retire-dash-tui]]). Related: [[feat-dash-stateless-mint-on-demand]], [[feat-dash-managed-component]], [[feat-dash-release-packaging]], [[feat-dash-side-by-side-dev]].
<!-- SECTION:NOTES:END -->
