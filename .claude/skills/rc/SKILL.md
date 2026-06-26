---
name: rc
description: Build a release-candidate of sextant from a branch/worktree and run it on the operator's LIVE setup, tiered from lightest to heaviest — an ephemeral side-by-side dash on a free port (no install, fully reversible, no sign-off), or a swap-in of the live brew binaries with a recorded, byte-faithful rollback. Use when the operator wants to try unreleased work on their live dash/bus before a real release, says "rc build", "run this on my live dash", "let me try the branch live", or "swap in / roll back the rc". NOT a release: it never pushes tags or the formula (those stay trusted-path, human-gated) — it gets unmerged code in front of the operator quickly and safely.
---

# rc — run a release-candidate on the live setup

The recurring need: get unreleased sextant work (usually a dash change) in front of
the operator on their **live bus** without waiting on a full brew release. A real
release is the right path for shipping, but it's heavy and tag/formula pushes are
trusted-path, human-gated. This skill is the fast lane: it builds from a branch and
runs it live, on the **lightest rung that proves the change**.

**You — the agent — drive this.** Never hand the operator a raw shell one-liner;
run the bundled runner (`rc.sh`, beside this file) yourself and narrate. The runner
owns the deterministic, reversible mechanics; you own the judgment and the warnings
before anything touches a live surface.

## The three rungs

| Rung | Command | Touches install? | Sign-off? | Use for |
|------|---------|------------------|-----------|---------|
| Ephemeral dash | `/rc dash <ref>` | no | no | fast dash UI / serve-path iteration |
| Swap-in | `/rc install <ref>` | yes (reversible) | no | full-fidelity: real PATH, launchd, components |
| Release | (not here) | — | **yes** | actually shipping → the release flow + your trusted-path tag |

`<ref>` is a branch name, a worktree path, or omitted (default: the worktree you're
working in). Resolve it to a clean worktree before building — if `<ref>` is a branch
with no worktree, make one (`git worktree add`).

The release rung is deliberately NOT in this skill: cutting a tag / bumping the
Homebrew formula is a production push the classifier blocks and the operator signs
off (see the live-sextant-via-release discipline). This skill stops at the live
swap; shipping is a separate, human-gated step.

## `/rc dash <ref>` — ephemeral side-by-side (default, safest)

This is the rung the dash-as-managed-component epic unlocked: because the web dash
is stateless at rest and every browser tab is its own co-equal client, a dash built
from any ref runs side-by-side on a free port against the **live bus** without
disturbing the managed one — no install, no taking prod down, A/B comparable.

1. Resolve `<ref>` → worktree `WT`.
2. Preflight: confirm the live bus is up and has a WebSocket listener (read `wsURL`
   from `<store>/bus.json`; if empty, warn — the page can't connect — and point at
   `sextant config set ws-listen 127.0.0.1:7423`, then stop).
3. `rc.sh dash <WT> <ref>` — builds `sextant-dash`, launches `--port 0 --ui
   <WT>/clients/sextant-dash/dashapi/web/app` against the live bus, prints the
   **URL** and tracks the pid/port.
4. Give the operator the URL. Remind them `/rc dash` is additive — their managed/
   prod dash is untouched, and they can open both and compare.
5. `--ui` serves the SPA from disk, so a frontend-only change just needs a browser
   refresh; a Go-side change needs a rebuild (`/rc dash` again).

Stop a dev dash with `/rc stop` (all) or `/rc stop <port>` (one). `/rc status` lists
what's running.

## `/rc install <ref>` — swap-in, with rollback

Higher fidelity: replaces the live brew `sextant*` binaries with the rc so the exact
PATH the operator's shell and launchd use is the rc — including the managed dash and
the other components. It is reversible: the runner records the exact stock symlink
targets before the first swap and `/rc rollback` restores them byte-for-byte.

1. Resolve `<ref>` → worktree `WT`.
2. `rc.sh build <WT>` — builds every `sextant*` binary the ref produces into the rc
   bin dir (`~/.sextant-rc/bin`).
3. **Verify gate (default on).** Before mutating the live install, prove the rc is
   sound: `cd <WT> && go vet ./... && go test ./bus/... ./clients/sextant-tui/internal/dash/... ./clients/sextant-dash/dashapi/... ./clients/sextant-dash/dashserve/... ./clients/sextant-cli/...`.
   If it's red, STOP and report — do not swap a broken rc onto the live machine.
   (Override only on explicit `--skip-verify`.)
4. **Warn, then swap the binaries.** Tell the operator plainly what's about to
   change: which binaries get repointed and that their version string will change
   (warn-before-killing-a-preview). On their go: `rc.sh swap`.
5. **Does the ref change the BUS?** The brew bus service runs the opt-path binary,
   which the bin-symlink swap does NOT touch — so a bus-side change (a new wire verb,
   an auth change, a protocol tweak) is NOT live until the bus itself is on the rc.
   If the ref touches `bus/` or `protocol/` (or you're unsure and the feature needs
   it — e.g. the managed dash's ADR-0047 delegated mint needs the `clients.session-operator`
   verb the bus serves), WARN that this briefly drops every bus client, then
   `rc.sh busswap`. It stops the stock brew bus and runs the rc bus on the same store
   (state persists; clients reconnect). Skip this for a CLI/dash-only change.
   Sanity-check the wire epoch is unchanged (`protocol/wire/frame.go` `Epoch`) before
   a bus swap — a bumped epoch means running stock clients can't reconnect.
6. **Re-point running components at the rc.** A swapped binary only takes effect for
   a component when it restarts. For each component currently `loaded + RUNNING`
   (`sextant components status`), `sextant components restart <name>` so it re-execs
   the rc binary. Narrate each restart (brief live interruption).
7. **Managed dash (opt-in).** If the ref adds the managed **dash** component and the
   operator wants it: it must be minted AGAINST the rc bus to carry its ADR-0047
   capability, so if you swapped the bus, first remove any stale stock-minted creds
   (`rm -f "$SEXTANT_HOME/components/dash.creds"`; `sextant context delete component-dash`)
   so `sextant components start dash` re-mints fresh. This is a NEW component stock
   didn't have, so `/rc rollback` must fully remove it (below). Default to an explicit ask.
8. Confirm: `sextant version` shows the rc build, `/rc status` shows SWAPPED (and
   `bus: RC` if you swapped the bus).

## `/rc rollback`

Restore stock in the REVERSE order of install — and crucially, tear down rc-only
components and the rc bus while the rc binary is STILL live (the stock binary won't
know the `dash` component or how to stop the rc bus):

1. **Stop + remove rc-only components FIRST** (while the rc `sextant` is still the
   swapped binary): for the dash component (rc-only — stock had none),
   `sextant components stop dash`, then remove its artifacts so a later install
   re-mints clean: `rm -f ~/Library/LaunchAgents/dev.sextant.dash.plist`,
   `rm -f "$SEXTANT_HOME/components/dash.creds"`, `sextant context delete component-dash`.
   Restart any STILL-RUNNING stock-era component (violet/workflow) after the binary
   restore in step 4 so it re-execs stock.
2. **`rc.sh busrestore`** — if the bus was swapped, stop the rc bus and bring the
   stock brew bus back (clients reconnect).
3. `rc.sh stop` — stop any ephemeral dev dashes.
4. **`rc.sh rollback`** — restore every recorded stock symlink target and remove any
   rc-only binary (e.g. `sextant-tui`). Then restart the components from step 1 so
   they re-exec stock.
5. Confirm: `sextant version` is back to the stock release; `/rc status` shows stock
   + `bus: stock`.

## `/rc status`

`rc.sh status` — the live `sextant` symlink + version, whether the install is
SWAPPED (and rollback is available), whether the bus is the rc or the stock brew
service, and any ephemeral dev dashes running.

## Safety invariants

- **Reversible always.** The swap records stock state once and never overwrites it
  while swapped, so rollback after several `/rc install`s still returns to the
  original release. Rollback is idempotent.
- **No production push, ever.** This skill never pushes a tag or the formula. If the
  operator wants to actually ship the rc, that's the release flow with their sign-off.
- **Warn before any live interruption.** Restarting a running component (or the dash)
  briefly drops it; say so before doing it. A bus swap (`busswap`) drops EVERY client
  at once — warn loudest before that one.
- **The rc bus is a tracked process, not KeepAlive.** While the bus is swapped it runs
  as a foreground process this skill tracks (it won't respawn if it crashes) — it is a
  test fixture, not the managed stock service. `busrestore` returns the KeepAlive brew
  bus. Don't leave a setup bus-swapped after a review session.
- **Verify before you swap.** A red build/test gate stops a live swap unless the
  operator explicitly overrides.
- **Pinned rc dir.** Everything lives under `~/.sextant-rc/` (bin + restore manifest
  + ephemeral state) — nothing leaks into the operator's real config root.
