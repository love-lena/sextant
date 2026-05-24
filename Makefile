# Sextant initial — build orchestration.
# Plain make. Targets: fmt, lint, test, build. All target the Go workspace.
# Plan: plans/bootstrap.md#M0

GO ?= go
GOLANGCI_LINT ?= golangci-lint
NILAWAY ?= nilaway

MODULE := github.com/love-lena/sextant-initial
PKGS := ./...

BIN_DIR := bin

# Binaries built by `make build`. New cmd/<name> dirs land here as milestones add them.
CMDS := sextant sextantd sextant-shipper sextant-natsboot sextant-clickhouseboot sextant-client-demo

.PHONY: all fmt lint lint-go lint-nilaway test build clean tidy install-tools

all: lint test

## fmt — format Go sources (gofumpt + goimports via golangci-lint).
fmt:
	$(GOLANGCI_LINT) fmt $(PKGS)

## lint — full lint: golangci-lint + nilaway. Both must pass.
lint: lint-go lint-nilaway

lint-go:
	$(GOLANGCI_LINT) run $(PKGS)

# nilaway is not bundled into golangci-lint v2; run it as its own command.
# Plan: plans/bootstrap.md#M0
lint-nilaway:
	$(NILAWAY) -include-pkgs="$(MODULE)" $(PKGS)

## test — go test with race detector.
test:
	$(GO) test -race -count=1 $(PKGS)

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
