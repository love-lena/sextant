# Onboarding guide + `make bootstrap` — design

**Status**: draft, awaiting user spec review
**Date**: 2026-05-26
**Driver**: Lena

## Problem

Two related gaps surfaced in the same request:

1. **The README is materially stale.** It still says "specifications, plans, and conventions only. No code yet" and frames the project as Phase 1/Phase 2 (classic Claude vs sextant agents). Phase 1 is merged on `main` (M0–M15) and the chat TUI is in. Anyone landing on the repo for the first time gets the wrong picture of what sextant is.
2. **Install is "easy in theory" but has hidden footguns.** A green-field operator has to know to install `nats-server`, `clickhouse`, `docker`/orbstack, Go ≥ 1.26, and `node`/`npm` before `sextantd` will start. `sextant doctor` doesn't preflight these — it only checks runtime reachability *after* the daemon is up — so the diagnostic surface for "missing host binary" is bad.

`PRINCIPLES.md` says: *"Rebuilding must be easy and fast … a single command, end to end."* `make install && sextant init && sextant start` is the closest we have, and it assumes every dep is already on PATH. This design closes that gap.

## Scope

In scope:
- Rewrite `README.md` as a layered operator-first / contributor-second onboarding guide.
- Add an "Onboarding help" pointer in `CLAUDE.md` so future agents direct users at the mdbook.
- Extend `sextant doctor` with host-binary preflight checks, and add a `--preflight` flag for the subset.
- Add `scripts/bootstrap.sh` + `make bootstrap` for green-field setup on macOS (Linux untested, partial).
- Update `docs/book/src/getting-started/install.md` so it documents both the automated (`make bootstrap`) and manual install paths.

Out of scope (separate follow-up tickets):
- Restructuring the rest of the mdbook (`docs/book/src/`) beyond `install.md`.
- Full Linux automation parity (apt doesn't ship `nats-server` or `clickhouse` in default repos).
- Pre-built binary releases (sextant currently distributed as git clone + `make install`).
- A `sextant bootstrap` Go subcommand (chicken-and-egg with `make install`).

## Approach

Approach B from brainstorming: doctor preflight for diagnosis AND a `make bootstrap` script for green-field setup. The bash script handles the toolchain bootstrap (which has to run before sextant exists); once `make install` produces the binary, the script calls `sextant doctor --preflight` for the richer Go-side verification.

Approaches A (preflight only) and C (Go-native `sextant bootstrap` subcommand) were rejected: A leaves the first `brew install` line as a manual step (defeats "single command"); C has a chicken-and-egg problem because the subcommand can't exist before `make install` runs.

## Design

### 1. README rewrite

Replace the current README contents end-to-end. New structure (top to bottom):

1. **What sextant is** — one paragraph. Go control plane for AI coding agents on NATS / ClickHouse / Docker. Drop the "no code yet" and Phase 1/Phase 2 framing.
2. **Quickstart (operator path)** — the four-to-five commands below:
   ```
   git clone git@github.com:love-lena/sextant.git
   cd sextant
   make bootstrap        # installs host deps, builds, installs, runs `sextant init`
   sextant start         # bring up the daemon
   sextant agents spawn assistant --template default
   sextant conversation assistant
   ```
   Followed by a short "what just happened" paragraph and a link to `docs/book/getting-started/first-run.md` for the deeper walkthrough.
3. **Verifying the install** — `sextant doctor`, summary of what it checks, and the macOS Gatekeeper `cp` warning (preserved verbatim — it's load-bearing operator knowledge).
4. **Where to go next (operators)** — link list into the mdbook: CLI reference, TUIs, templates, worktrees, troubleshooting.
5. **Contributing** — short pointer to `PRINCIPLES.md`, `conventions/`, `plans/bootstrap.md`, `make test` / `make lint`, the `EnterWorktree` tool convention, the commit footer rule.
6. **Pilot footer** — one line linking `love-lena/sextant-pilot` for historical interest.

Tone: terse, command-first, link out for depth.

### 2. CLAUDE.md addition

New section near the top of `CLAUDE.md` (this is the auto-loaded project doc):

```markdown
## Helping someone onboard

If the user asks how to get started with sextant, how to install it,
or how to drive it for the first time, point them at:

- README.md — the one-page quickstart
- docs/book/getting-started/{install,first-run,repo-tour}.md —
  the deeper walkthrough (run `mdbook serve docs/book` to browse
  in a browser, or open the .md files directly)

Don't reinvent install instructions inline. The mdbook is the
source of truth for the installed-and-running flow; the README is
the source of truth for the quickstart.
```

Rationale: future Claude Code (and other agents) will be the ones answering onboarding questions when Lena's not around. Pin them to canonical docs.

### 3. `sextant doctor` preflight checks

Extend `cmd/sextant/doctor.go`. Add a new check `kind = "host-dep"` with one row per required external binary, sequenced before the existing `config` / `data-dir` / `daemon` / `nats` / `clickhouse` rows:

| Check          | Probe                                                  | Remedy on fail                                                           |
|----------------|--------------------------------------------------------|--------------------------------------------------------------------------|
| `nats-server`  | `exec.LookPath("nats-server")`; `nats-server --version` if found | `brew install nats-server` (macOS) / "download from github.com/nats-io/nats-server/releases" (Linux) |
| `clickhouse`   | `exec.LookPath("clickhouse")`; `clickhouse --version`  | `brew install clickhouse` (macOS) / official apt repo URL (Linux)        |
| `docker` (binary) | `exec.LookPath("docker")`                           | `brew install --cask orbstack` (macOS) / `apt install docker.io` (Linux) |
| `docker` (daemon) | `docker info` succeeds                              | `start OrbStack / Docker Desktop`                                        |
| `go`           | (`--contributor`-only) `go version` and ≥ 1.26         | `brew install go` / official Go installer                                |
| `node` / `npm` | (`--contributor`-only) `node --version`, `npm --version` | `brew install node` / `apt install nodejs npm`                         |

New flags:
- `sextant doctor --preflight` — runs **only** the host-dep checks. Does not require config to exist (so it can be called from `bootstrap.sh` right after `make install` and before `sextant init`).
- `sextant doctor --contributor` — additionally checks `go`, `node`, `npm`. Off by default.

The two flags compose: `sextant doctor --preflight --contributor` = host-dep checks including contributor-only deps, no daemon/config checks.

Distinguishing "docker binary missing" from "docker daemon not running" matters because the remedy is different — and "daemon not running" is the more common failure mode after a laptop reboot.

Exit codes: 0 on green, 2 on any failure. Matches existing convention (`cmd/sextant/main.go:104-108`).

### 4. `scripts/bootstrap.sh` + `make bootstrap`

A bash script (~150 lines) at `scripts/bootstrap.sh`. Bash is the right choice because the script must run *before* any sextant binary exists.

**CLI surface:**
- `./scripts/bootstrap.sh` — interactive, prompts before installing.
- `./scripts/bootstrap.sh --yes` / `-y` — non-interactive (CI, repeat runs).
- `./scripts/bootstrap.sh --skip-init` — install deps + `make install`, but don't run `sextant init`.
- `./scripts/bootstrap.sh --help`.

**Script flow:**

1. **Hard prerequisites.** Verify `make`, `git`, `uname` are present. Fail loud if missing — these come from xcode-select (macOS) or build-essential (Linux); we can't `brew install` them for the user.
2. **Detect package manager.** macOS → `brew`. Linux → prefer `apt-get`, fall back to `brew` (linuxbrew). Fail if neither.
3. **Audit phase (no side effects).** Build a list of what's missing:
   - Go and `go version` ≥ 1.26 (compare with `sort -V`)
   - `nats-server`, `clickhouse`, `docker`, `node`
   - Docker daemon reachability (`docker info`). Distinct from binary presence: if the docker binary is missing, the plan includes an install action; if the binary is present but the daemon is down, the plan includes no install action — just a `note:` line in the plan output telling the operator to start OrbStack manually. The bootstrap continues either way (init and make install don't touch the daemon); `sextant start` later is where a stopped daemon will surface as a real failure.
4. **Plan + confirm.** Print the plan verbatim — one line per intended action. Examples:
   ```
   === sextant bootstrap plan ===
   Package manager: brew
   Note: Linux path is unverified; macOS is the tested target.

     - install Go (>= 1.26)
     - install nats-server
     - install clickhouse
     - install orbstack (docker; install Docker Desktop manually if you prefer)
     - make install
     - sextant init

   Proceed? [Y/n]
   ```
   If everything's present, skip the prompt and just print `All dependencies present.` followed by the remaining steps.
5. **Install Go first.** Required before `make install` runs.
6. **Install runtime deps.** `nats-server`, `clickhouse`, `docker`, `node` (only what's missing).
7. **`make install`.**
8. **`sextant doctor --preflight`.** Now that the binary exists, run the Go-side check. Catches docker-daemon-stopped, version mismatches, anything brew couldn't verify. If it fails, exit non-zero with the doctor output as the diagnostic.
9. **`sextant init`** (unless `--skip-init`).
10. **Final line:** `Bootstrap complete. Next: sextant start && sextant doctor`.

**Makefile target:**

```make
## bootstrap — green-field setup: host deps + build + install + init.
##             Interactive. Pass YES=1 for non-interactive (CI / repeat runs).
bootstrap:
	@bash scripts/bootstrap.sh $(if $(YES),--yes,)
```

**Linux scope (honest about limits).** `nats-server` and `clickhouse` aren't in default apt repos. If those are missing on Linux, the script prints the upstream download URL and exits non-zero rather than failing partway through. Linux operators get partial automation; macOS gets full. The script output, README, and mdbook all carry a "Linux is unverified" note.

**Idempotence.** Re-running after `git pull`: brew steps are no-ops, `make install` rebuilds, `sextant init` skips existing files. Re-running after a failed bootstrap: every step probes state before acting. `make bootstrap YES=1` is safe in CI setup phase.

### 5. mdbook install.md update

Restructure `docs/book/src/getting-started/install.md` so the automated path is the lead and the manual path is documented below:

1. **Automated install (recommended).** One paragraph + the `make bootstrap` invocation. Links to `scripts/bootstrap.sh` source for "what does it actually do." Carries the "Linux unverified" caveat.
2. **Manual install.** The existing dep table and per-dep `brew install` commands, framed as "if you'd rather install everything yourself, or you're on Linux." Includes the Gatekeeper / `cp` warning that's currently in the README.
3. **Verifying.** `make lint test` + `sextant doctor --preflight`.
4. **Sidecar image** (unchanged).
5. **Snapshot version reporting** (unchanged).

Rationale: someone who wants the magic gets one command. Someone who wants to know what's happening can read the manual path. Anyone hitting a brew quirk on a corp machine has a documented escape hatch.

### 6. Testing

- **Doctor preflight unit tests.** `cmd/sextant/doctor_test.go` gets new cases. Mock `exec.LookPath` (pass a fake PATH via env or inject a lookup function) and assert each `CheckResult` has the right `Status` and `Remedy`. Cases: all-present, nats-server missing, docker binary present but daemon unreachable, contributor mode with Go too old.
- **`shellcheck scripts/bootstrap.sh`** in CI. Catches the usual bash footguns.
- **macOS smoke test.** Optional GitHub Actions job that boots a fresh `macos-latest` runner, runs `make bootstrap YES=1`, and asserts `sextant doctor` ends green. Gated as a separate CI job (like `sidecar-image`), not wired into `make test` — heavy but high-signal. Acceptable to defer this to a follow-up PR if the initial work is already large.
- **No tests for the brew install calls themselves.** We trust brew. Upstream package renames surface from the smoke run.

### 7. Risks and follow-ups

**Risks:**
- macOS brew rate-limits or upstream package renames break `make bootstrap` silently. Mitigation: the smoke-test CI job catches it.
- An operator on a corp Mac with brew locked down hits an opaque `brew install` failure. Mitigation: the manual-install path in the mdbook is fully documented; the script's failure output points there.
- Auto-installing OrbStack on a machine that already has Docker Desktop sets up a conflict. Mitigation: the `docker` check sees the existing binary on PATH, skips the install, and only complains if the daemon isn't reachable.

**Follow-up tickets to file:**
- Full Linux apt parity (`nats-server`, `clickhouse` via upstream apt repos).
- Pre-built binary release pipeline so `make install` isn't the only path.
- mdbook restructure beyond `install.md` (rest of `getting-started/` and `operator-guide/` could benefit from a similar layered treatment).
- `sextant doctor --preflight --json` for machine consumption (matches existing `--json` pattern).

## Acceptance

This work is done when:

1. `README.md` is rewritten per section 1; pointed-at links resolve; the "no code yet" framing is gone.
2. `CLAUDE.md` has the new "Onboarding help" section.
3. `sextant doctor` shows `host-dep` rows by default; `sextant doctor --preflight` runs only those rows; both exit non-zero on any missing dep.
4. `make bootstrap` on a fresh macOS machine (or one missing one or more deps) brings everything to a state where `sextant doctor` ends green, prompting interactively before brew-installing anything.
5. `make bootstrap YES=1` does the same non-interactively.
6. `docs/book/src/getting-started/install.md` is restructured per section 5.
7. `shellcheck scripts/bootstrap.sh` passes.
8. Doctor preflight unit tests pass under `make test`.
9. (Optional, defer-able) GitHub Actions macOS smoke job runs `make bootstrap YES=1` clean.

## Related

- `PRINCIPLES.md` — "Rebuilding must be easy and fast" and "User ergonomics is a first-class deliverable" are the load-bearing values this work serves.
- `plans/issues/feat-make-install-target.md` (resolved) — earlier ergonomic work in the same area; this design builds on it.
- `plans/issues/feat-doctor-stale-binary-detection.md` (resolved) — the preflight pattern in section 3 mirrors the stale-binary check.
- `plans/issues/docs-install-via-make-install-not-cp.md` — the Gatekeeper `cp` warning preserved in README + mdbook.
