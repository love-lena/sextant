# Sextant initial — build orchestration.
# Plain make. Targets: fmt, lint, test, build. All target the Go workspace.
# Plan: plans/bootstrap.md#M0

GO ?= go
GOLANGCI_LINT ?= golangci-lint
NILAWAY ?= nilaway
NPM ?= npm

MODULE := github.com/love-lena/sextant-initial
PKGS := ./...

BIN_DIR := bin
TS_DIR := clients/typescript

# Binaries built by `make build`. New cmd/<name> dirs land here as milestones add them.
CMDS := sextant sextantd sextant-shipper sextant-natsboot sextant-clickhouseboot sextant-client-demo

.PHONY: all fmt lint lint-go lint-nilaway lint-ts test test-go test-ts build clean tidy install-tools \
        ts-install ts-codegen ts-lint ts-test ts-build

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

## test — go test with race detector + TS integration suite.
test: test-go test-ts

test-go:
	$(GO) test -race -count=1 $(PKGS)

# test-ts — vitest run for @sextant/client (spawns nats-server in-process).
# Plan: plans/bootstrap.md#M8
test-ts: ts-install
	cd $(TS_DIR) && $(NPM) test

# ts-* targets — TypeScript client maintenance.
ts-install:
	@cd $(TS_DIR) && [ -d node_modules ] || $(NPM) ci

ts-codegen: ts-install
	cd $(TS_DIR) && $(NPM) run codegen

ts-lint: ts-install
	cd $(TS_DIR) && $(NPM) run lint

ts-test: ts-install
	cd $(TS_DIR) && $(NPM) test

ts-build: ts-install
	cd $(TS_DIR) && $(NPM) run build

## build — compile every command under cmd/.
build: $(BIN_DIR)
	@if [ -z "$(CMDS)" ]; then echo "no cmds to build yet"; exit 0; fi
	@for cmd in $(CMDS); do \
	  echo ">> build $$cmd"; \
	  $(GO) build -o $(BIN_DIR)/$$cmd ./cmd/$$cmd || exit 1; \
	done

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

## tidy — go mod tidy.
tidy:
	$(GO) mod tidy

## install-tools — pulls golangci-lint and nilaway. Idempotent.
install-tools:
	@command -v $(GOLANGCI_LINT) >/dev/null || brew install golangci-lint
	@command -v $(NILAWAY) >/dev/null || $(GO) install go.uber.org/nilaway/cmd/nilaway@latest

clean:
	rm -rf $(BIN_DIR)
