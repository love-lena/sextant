# Sextant — build orchestration.
# Plain make. Targets: fmt, lint, test, build. All target the Go workspace.
# Plan: plans/bootstrap.md#M0

GO ?= go
GOLANGCI_LINT ?= golangci-lint
NILAWAY ?= nilaway
NPM ?= npm

MODULE := github.com/love-lena/sextant
PKGS := ./...

BIN_DIR := bin
TS_DIR := clients/typescript
SIDECAR_DIR := images/sidecar/entrypoint

# install / uninstall destination. Override PREFIX for system-wide installs,
# e.g. `sudo make install PREFIX=/usr/local`.
PREFIX ?= $(HOME)/.local
INSTALL_DIR := $(PREFIX)/bin

# Binaries built by `make build`. New cmd/<name> dirs land here as milestones add them.
CMDS := sextant sextantd sextant-shipper sextant-natsboot sextant-clickhouseboot sextant-client-demo sextant-tui-agents

.PHONY: all fmt lint lint-go lint-nilaway lint-ts lint-sidecar test test-go test-ts test-sidecar build clean tidy install install-tools uninstall bootstrap \
        ts-install ts-codegen ts-lint ts-test ts-build \
        sidecar-install sidecar-image sidecar-image-test backlog-install

all: lint test

## fmt — format Go sources (gofumpt + goimports via golangci-lint).
fmt:
	$(GOLANGCI_LINT) fmt $(PKGS)

## lint — full lint: golangci-lint + nilaway + TS strict tsc. Both must pass.
lint: lint-go lint-nilaway lint-ts

lint-go:
	$(GOLANGCI_LINT) run $(PKGS)

# nilaway is not bundled into golangci-lint v2; run it as its own command.
# Plan: plans/bootstrap.md#M0
lint-nilaway:
	$(NILAWAY) -include-pkgs="$(MODULE)" $(PKGS)

# lint-ts — TypeScript strict type-check (tsc --noEmit) for @sextant/client.
# Mirrors the Go lint gate; failures block merge alongside Go lint.
# Plan: plans/bootstrap.md#M8
lint-ts: ts-install
	cd $(TS_DIR) && $(NPM) run lint

# lint-sidecar — tsc --noEmit for the sidecar entrypoint (includes test/ dir).
# Depends on ts-build because @sextant/client is resolved through the npm
# workspace at the repo root, and the sidecar's tsc reads its types from
# clients/typescript/dist/index.d.ts.
lint-sidecar: sidecar-install ts-build
	cd $(SIDECAR_DIR) && $(NPM) run lint

## test — go test with race detector + TS integration suite + sidecar unit tests.
test: test-go test-ts test-sidecar

test-go:
	$(GO) test -race -count=1 $(PKGS)

# test-ts — vitest run for @sextant/client (spawns nats-server in-process).
# Plan: plans/bootstrap.md#M8
test-ts: ts-install
	cd $(TS_DIR) && $(NPM) test

# test-sidecar — vitest unit tests for the sidecar entrypoint classifier.
# No external services required; runs entirely in-process.
# Depends on ts-build so `import "@sextant/client"` resolves through the npm
# workspace to clients/typescript/dist/.
test-sidecar: sidecar-install ts-build
	cd $(SIDECAR_DIR) && $(NPM) test

# ts-install / sidecar-install — install all npm workspace deps from the
# repo root. The root package.json declares `clients/typescript` and
# `images/sidecar/entrypoint` as workspaces, so a single `npm install`
# wires `@sextant/client` into the sidecar via the workspace graph instead
# of the (formerly broken) checked-in symlink.
ts-install:
	@[ -d node_modules ] || $(NPM) ci

sidecar-install: ts-install

ts-codegen: ts-install
	cd $(TS_DIR) && $(NPM) run codegen

ts-lint: ts-install
	cd $(TS_DIR) && $(NPM) run lint

ts-test: ts-install
	cd $(TS_DIR) && $(NPM) test

ts-build: ts-install
	cd $(TS_DIR) && $(NPM) run build

## build — compile every command under cmd/.
#
# VERSION is read from the top-level VERSION file (source of truth per
# feat-semver-versioning); GIT_SHORT is `git rev-parse --short HEAD`; GIT_SHA
# is the full HEAD. All three are baked into pkg/version via -ldflags so
# `sextant version`, `sextantd version`, and `sextant doctor` (staleness
# check) all see consistent values. The `?=` lets CI / packagers override.
# Each falls back to a safe default when the source is missing:
#   - VERSION file missing → "dev"
#   - not a git checkout    → "unknown" (and empty GIT_SHA → doctor skips
#     the staleness check rather than erroring).
VERSION   ?= $(shell cat VERSION 2>/dev/null || echo dev)
GIT_SHA   ?= $(shell git rev-parse HEAD 2>/dev/null)
GIT_SHORT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_LDFLAGS := \
  -X github.com/love-lena/sextant/pkg/version.Version=$(VERSION) \
  -X github.com/love-lena/sextant/pkg/version.Commit=$(GIT_SHORT) \
  -X github.com/love-lena/sextant/pkg/version.GitSHA=$(GIT_SHA)

build: $(BIN_DIR)
	@if [ -z "$(CMDS)" ]; then echo "no cmds to build yet"; exit 0; fi
	@for cmd in $(CMDS); do \
	  echo ">> build $$cmd"; \
	  $(GO) build -ldflags "$(BUILD_LDFLAGS)" -o $(BIN_DIR)/$$cmd ./cmd/$$cmd || exit 1; \
	done

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

## install — build then copy every $(CMDS) binary into $(INSTALL_DIR).
## Override PREFIX for non-default destinations (default: $(HOME)/.local).
install: build
	@mkdir -p $(INSTALL_DIR)
	@for cmd in $(CMDS); do \
	  install -m 0755 $(BIN_DIR)/$$cmd $(INSTALL_DIR)/$$cmd; \
	  echo ">> installed $(INSTALL_DIR)/$$cmd"; \
	done

## uninstall — remove every $(CMDS) binary from $(INSTALL_DIR).
uninstall:
	@for cmd in $(CMDS); do \
	  rm -f $(INSTALL_DIR)/$$cmd; \
	  echo ">> removed $(INSTALL_DIR)/$$cmd"; \
	done

## tidy — go mod tidy.
tidy:
	$(GO) mod tidy

## install-tools — pulls golangci-lint and nilaway. Idempotent.
install-tools:
	@command -v $(GOLANGCI_LINT) >/dev/null || brew install golangci-lint
	@command -v $(NILAWAY) >/dev/null || $(GO) install go.uber.org/nilaway/cmd/nilaway@latest

## backlog-install — installs the pinned Backlog.md CLI used to manage tickets
##                    in backlog/. Idempotent; invoked by bootstrap. Run the
##                    binary as tools/backlog/node_modules/.bin/backlog.
backlog-install:
	@[ -d tools/backlog/node_modules ] || $(NPM) --prefix tools/backlog ci

## bootstrap — green-field setup: host deps + build + install + init.
##             Interactive; prompts before brew-installing. Pass YES=1 for
##             non-interactive (CI / repeat runs). Pass SKIP_INIT=1 to
##             skip `sextant init`.
bootstrap:
	@bash scripts/bootstrap.sh \
	  $(if $(YES),--yes,) \
	  $(if $(SKIP_INIT),--skip-init,)

clean:
	rm -rf $(BIN_DIR)

## screenshots: Render every tests/visual/*.tape via VHS (Docker) and
##              drop the PNGs into screenshots/. Used by the
##              design-loop workflow per feat-tui-vhs-fixture-design-loop.
##              Requires Docker; CI gets the same loop via the
##              ghcr.io/charmbracelet/vhs image.
screenshots:
	@mkdir -p screenshots
	@for tape in tests/visual/*.tape; do \
	  echo "vhs $$tape"; \
	  docker run --rm -v "$$(pwd):/vhs" -w /vhs ghcr.io/charmbracelet/vhs "$$tape" || exit 1; \
	done

# ---------------------------------------------------------------------------
# Sidecar container image. Requires a working `docker` (OrbStack on macOS).
# Intentionally NOT wired into `make test`: the image build pulls multi-MB
# tarballs and won't work on CI runners without Docker, so it stays opt-in.
# CI exercises this via the dedicated `sidecar-image` GitHub Actions job.
# Plan: plans/bootstrap.md#M9
# Spec: specs/components/sidecar-image.md
# ---------------------------------------------------------------------------

# sidecar-image — build sextant-sidecar:<git-sha> and sextant-sidecar:latest.
# Stages clients/typescript/dist into the build context via `ts-build` so the
# image's npm install can resolve the local @sextant/client file dep.
sidecar-image: ts-build
	docker build -f images/sidecar/Dockerfile \
		-t sextant-sidecar:$$(git rev-parse HEAD) \
		-t sextant-sidecar:latest \
		.

# sidecar-image-test — full M9 acceptance smoke. See images/sidecar/test.sh.
sidecar-image-test:
	bash images/sidecar/test.sh
