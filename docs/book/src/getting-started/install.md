# Install

There are two paths: the **automated path** (one command, prompts before installing anything) and the **manual path** (install every dep yourself, then build). The automated path is what most operators want.

> macOS via Homebrew is the tested target. Linux is partial — `nats-server` and `clickhouse` aren't in default apt repos, and the bootstrap script will bail with upstream URLs if they're missing.

## Automated

From a fresh checkout:

```bash
make bootstrap
```

This calls [`scripts/bootstrap.sh`](https://github.com/love-lena/sextant/blob/main/scripts/bootstrap.sh), which:

1. Audits host deps (Go ≥ 1.26, `nats-server`, `clickhouse`, `docker`/OrbStack, `node`)
2. Prints the install plan and prompts `Y/n`
3. `brew install`s whatever's missing (macOS) — OrbStack as the docker default
4. Runs `make install` (builds and installs every `cmd/` binary to `~/.local/bin`)
5. Runs `sextant doctor --preflight` to confirm
6. Runs `sextant init` to generate config, CA, and the default template

Pass `YES=1` for non-interactive use (CI, repeat runs). Pass `SKIP_INIT=1` to skip step 6 if you'd rather manage `~/.config/sextant/` yourself.

Re-running after `git pull` is safe: brew steps are no-ops, `make install` rebuilds, `sextant init` is idempotent.

## Manual

If you'd rather install everything yourself (or you're on Linux):

### Host dependencies

| Dependency      | Why                                                                                 | Install                                                                |
|-----------------|-------------------------------------------------------------------------------------|------------------------------------------------------------------------|
| **Go ≥ 1.26**   | Module declares `go 1.26` (`go.mod:3`). Older toolchains will refuse to build.      | macOS: `brew install go`. Linux: see <https://go.dev/dl>.              |
| **NATS server** | `sextantd` execs it as a subprocess.                                                | macOS: `brew install nats-server`. Linux: <https://github.com/nats-io/nats-server/releases>. |
| **ClickHouse server** | `sextantd` execs it as a subprocess.                                          | macOS: `brew install clickhouse`. Linux: <https://clickhouse.com/docs/en/install>. |
| **Docker** (OrbStack on macOS) | Each agent runs in a container.                                          | `brew install --cask orbstack` or Docker Desktop.                      |
| **Node + npm**  | Building the TypeScript client + the sidecar image.                                 | macOS: `brew install node`. Linux: `apt install nodejs npm`.            |
| **`golangci-lint`, `nilaway`** | CI gates — only needed if you intend to run `make lint`.              | `make install-tools` (`Makefile:134`) installs both.                   |

The host must have a container runtime — there is no bare-process fallback for the sidecar (`specs/architecture.md` §3).

### Build and install

From a checkout:

```bash
make install
```

`make install` builds every binary under `cmd/` and writes them to `$PREFIX/bin` (default `$HOME/.local/bin`; `Makefile:19-20,116-120`). The `CMDS` variable at `Makefile:23` is the authoritative list: `sextant`, `sextantd`, `sextant-shipper`, `sextant-natsboot`, `sextant-clickhouseboot`, `sextant-client-demo`, `sextant-tui-agents`.

Override the destination for a system-wide install:

```bash
sudo make install PREFIX=/usr/local
```

The Makefile uses `/usr/bin/install` rather than `cp`. On macOS, plain `cp` stamps `com.apple.provenance` onto the destination, and Gatekeeper then SIGKILLs the resulting binary on launch (exit 137, no stderr). `make install` sidesteps that. Cross-reference: `plans/issues/docs-install-via-make-install-not-cp.md`. Linux is unaffected.

`make uninstall` removes the installed binaries (`Makefile:122-127`).

### Generate config

```bash
sextant init
```

See [First run](./first-run.md) for what this writes.

## Build the sidecar image

The agent container image is built separately because it's an opt-in multi-MB pull (`Makefile:142-157`):

```bash
make sidecar-image
```

This produces `sextant-sidecar:<git-sha>` and `sextant-sidecar:latest` (`Makefile:153-157`). The build is **not** wired into `make test`; CI exercises it through a dedicated job.

To verify the image:

```bash
make sidecar-image-test
```

This runs `images/sidecar/test.sh`, which asserts the image builds, every required tool is on PATH (node, npm, git, gh, jq, yq, rg, fzf, curl, wget, make, gcc, python3, go, vim — `images/sidecar/test.sh:42-51`), and the entrypoint binary is present. It also emits a non-fatal warning if the image exceeds 3 GiB (target is `< 2 GiB`).

## Verify the build

```bash
make lint test
```

`make lint` runs three gates (`Makefile:36`): Go (`golangci-lint`), null-pointer analysis (`nilaway`, run separately because it isn't bundled into golangci-lint v2 — `Makefile:43-44`), and TypeScript (`tsc --noEmit`) for both the client and the sidecar entrypoint.

`make test` runs `go test -race -count=1 ./...` plus the TypeScript vitest suites for the client and the sidecar (`Makefile:57-70`).

A faster sanity check that doesn't need the full test suite:

```bash
sextant doctor --preflight
```

Runs only the host-binary checks; useful right after a fresh install or after a laptop reboot to confirm Docker is up.

## Snapshot version reporting

`pkg/version` exposes `GitSHA`, populated via `-ldflags` from the build's `git rev-parse HEAD` (`Makefile:100-101`). `sextant doctor` reads it back to detect stale installed binaries (cross-referenced from `plans/issues/feat-doctor-stale-binary-detection.md`).
