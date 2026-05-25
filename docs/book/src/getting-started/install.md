# Install

## What you need on the host

| Dependency      | Why                                                                                 | Notes                                                                  |
|-----------------|-------------------------------------------------------------------------------------|------------------------------------------------------------------------|
| **Go ≥ 1.26**   | Module declares `go 1.26` (`go.mod:3`). Older toolchains will refuse to build.      | macOS: `brew install go` (verify version with `go version`).           |
| **NATS server** | `sextantd` execs it as a subprocess.                                                | `brew install nats-server`.                                            |
| **ClickHouse server** | `sextantd` execs it as a subprocess.                                          | `brew install clickhouse`.                                             |
| **Docker** (OrbStack on macOS) | Each agent runs in a container.                                          | `brew install --cask orbstack` or Docker Desktop.                      |
| **Node + npm**  | Building the TypeScript client + the sidecar image.                                 | Bundled with the sidecar image; only needed on the build host.         |
| **`golangci-lint`, `nilaway`** | CI gates — only needed if you intend to run `make lint`.              | `make install-tools` (`Makefile:134`) installs both.                   |

The host must have a container runtime — there is no bare-process fallback for the sidecar (`specs/architecture.md` §3).

## Build and install

From a checkout:

```bash
make install
```

`make install` builds every binary under `cmd/` and writes them to `$PREFIX/bin` (default `$HOME/.local/bin`; `Makefile:19-20,116-120`). The `CMDS` variable at `Makefile:23` is the authoritative list of binaries: `sextant`, `sextantd`, `sextant-shipper`, `sextant-natsboot`, `sextant-clickhouseboot`, `sextant-client-demo`, `sextant-tui-agents`.

Override the destination for a system-wide install:

```bash
sudo make install PREFIX=/usr/local
```

The Makefile uses `/usr/bin/install` rather than `cp`. On macOS, plain `cp` stamps the `com.apple.provenance` xattr onto the destination, and Gatekeeper then SIGKILLs the resulting binary on launch (exit 137, no stderr). `make install` sidesteps that. The cross-reference is `plans/issues/docs-install-via-make-install-not-cp.md`. Linux is unaffected.

`make uninstall` removes the installed binaries (`Makefile:122-127`).

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

## Snapshot version reporting

`pkg/version` exposes `GitSHA`, populated via `-ldflags` from the build's `git rev-parse HEAD` (`Makefile:100-101`). `sextant doctor` reads it back to detect stale installed binaries (cross-referenced from `plans/issues/feat-doctor-stale-binary-detection.md`).
